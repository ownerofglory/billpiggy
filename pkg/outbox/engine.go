package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures polling, retry and observability for one engine.
type Options struct {
	// WorkerID identifies the lease holder. It defaults to the hostname and
	// process id, which is enough to tell replicas apart in logs.
	WorkerID string
	// Policy controls retries and dead-lettering.
	Policy Policy
	// IdleInterval is the sleep between polls when the queue is empty. It
	// defaults to two seconds.
	IdleInterval time.Duration
	// MaxDrain bounds how many messages Run processes back-to-back before
	// yielding, so one busy subscription cannot starve its own poll loop. It
	// defaults to 100.
	MaxDrain int
	// Logger receives structured progress and failure records.
	Logger *slog.Logger
	// Clock is injectable so tests can assert on timestamps deterministically.
	Clock func() time.Time
}

// Engine polls a Store on behalf of one Handler.
type Engine struct {
	store   Store
	handler Handler
	options Options

	processed    atomic.Uint64
	retried      atomic.Uint64
	deadLettered atomic.Uint64
	lag          atomic.Int64

	mu            sync.Mutex
	lastSuccessAt time.Time
	lastFailureAt time.Time
	lastError     string
}

// Stats reports engine progress for metrics and readiness probes.
type Stats struct {
	// Subscription is the handler's durable name.
	Subscription string
	// Processed counts messages committed since start.
	Processed uint64
	// Retried counts failed deliveries scheduled for another attempt.
	Retried uint64
	// DeadLettered counts messages abandoned after exhausting their attempts.
	DeadLettered uint64
	// LastSuccessAt is when a message was last committed.
	LastSuccessAt time.Time
	// LastFailureAt is when a delivery last failed.
	LastFailureAt time.Time
	// LastError is the most recent handler failure message.
	LastError string
	// Lag is the pending message count observed at the last poll.
	Lag int64
}

// NewEngine creates an engine for one handler, filling unset options with
// their defaults.
func NewEngine(store Store, handler Handler, options Options) (*Engine, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	if handler == nil {
		return nil, ErrNoHandler
	}
	if handler.Name() == "" {
		return nil, errors.New("outbox handler must have a name")
	}
	options.Policy = options.Policy.normalise()
	if options.WorkerID == "" {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		options.WorkerID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	if options.IdleInterval <= 0 {
		options.IdleInterval = 2 * time.Second
	}
	if options.MaxDrain <= 0 {
		options.MaxDrain = 100
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &Engine{store: store, handler: handler, options: options}, nil
}

// Name returns the subscription the engine drives.
func (e *Engine) Name() string { return e.handler.Name() }

// Step processes at most one message and reports its outcome. Tests drive an
// engine through Step rather than Run so they stay deterministic.
func (e *Engine) Step(ctx context.Context) (Result, error) {
	result, err := e.store.ProcessNext(ctx, e.handler.Name(), e.handler.AggregateTypes(), e.options.Policy, e.options.WorkerID, e.handler.Handle)
	if err != nil {
		e.recordFailure(err)
		return result, err
	}
	e.record(result)
	return result, nil
}

// Run drives the engine until ctx is cancelled, returning ctx.Err().
//
// It drains available messages back-to-back and only sleeps for IdleInterval
// once the queue reports Idle, so a burst is consumed without waiting out a
// full poll interval per message.
func (e *Engine) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		drained := 0
		for drained < e.options.MaxDrain {
			result, err := e.Step(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				e.options.Logger.Error("outbox step failed", "subscription", e.handler.Name(), "error", err)
				break
			}
			if result.Status == Idle {
				break
			}
			drained++
		}
		e.refreshLag(ctx)
		timer.Reset(e.options.IdleInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// refreshLag records the pending count, ignoring transient store errors so a
// lag query never interrupts processing.
func (e *Engine) refreshLag(ctx context.Context) {
	lag, err := e.store.Lag(ctx, e.handler.Name())
	if err != nil {
		return
	}
	e.lag.Store(lag)
}

func (e *Engine) record(result Result) {
	switch result.Status {
	case Processed:
		e.processed.Add(1)
		e.mu.Lock()
		e.lastSuccessAt = e.options.Clock()
		e.mu.Unlock()
	case Retried:
		e.retried.Add(1)
		e.recordFailure(result.Err)
		e.options.Logger.Warn("outbox message retried", "subscription", e.handler.Name(),
			"event_type", result.Message.EventType, "event_id", result.Message.EventID,
			"attempts", result.Message.Attempts, "error", result.Err)
	case DeadLettered:
		e.deadLettered.Add(1)
		e.recordFailure(result.Err)
		e.options.Logger.Error("outbox message dead-lettered", "subscription", e.handler.Name(),
			"event_type", result.Message.EventType, "event_id", result.Message.EventID,
			"attempts", result.Message.Attempts, "error", result.Err)
	}
}

func (e *Engine) recordFailure(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastFailureAt = e.options.Clock()
	if err != nil {
		e.lastError = err.Error()
	}
}

// Stats returns a snapshot of the engine's counters.
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{
		Subscription:  e.handler.Name(),
		Processed:     e.processed.Load(),
		Retried:       e.retried.Load(),
		DeadLettered:  e.deadLettered.Load(),
		LastSuccessAt: e.lastSuccessAt,
		LastFailureAt: e.lastFailureAt,
		LastError:     e.lastError,
		Lag:           e.lag.Load(),
	}
}

// Health returns a readiness check that fails when the subscription has
// messages pending but has not committed one within staleness, or when any
// message has been dead-lettered.
//
// It returns a bare function so this package never depends on the health
// registry; callers register it themselves.
func (e *Engine) Health(staleness time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		stats := e.Stats()
		if stats.DeadLettered > 0 {
			return fmt.Errorf("subscription %s dead-lettered %d messages: %s", stats.Subscription, stats.DeadLettered, stats.LastError)
		}
		lag, err := e.store.Lag(ctx, stats.Subscription)
		if err != nil {
			return fmt.Errorf("read %s lag: %w", stats.Subscription, err)
		}
		e.lag.Store(lag)
		if lag == 0 {
			return nil
		}
		last := stats.LastSuccessAt
		if last.IsZero() {
			last = stats.LastFailureAt
		}
		if !last.IsZero() && e.options.Clock().Sub(last) > staleness {
			return fmt.Errorf("subscription %s has %d pending messages and has not progressed since %s", stats.Subscription, lag, last.Format(time.RFC3339))
		}
		return nil
	}
}
