// Package outbox drives transactional-outbox subscriptions with leasing,
// exponential retry, dead-lettering and progress reporting.
//
// The package holds no business knowledge and depends only on the standard
// library. A [Store] supplies durable delivery semantics, a [Handler] applies
// messages to one read model, and an [Engine] joins the two: it polls the
// store, hands each message to the handler inside the store's transaction, and
// records the outcome.
//
// Messages are processed one transaction at a time, never in batches, so a
// single failing event cannot roll back the progress of its neighbours.
package outbox

import (
	"context"
	"errors"
	"time"
)

// Message is one outbox entry handed to a handler.
type Message struct {
	// OutboxID identifies the delivery row for this subscription.
	OutboxID string
	// EventID identifies the source event.
	EventID string
	// GlobalSeq is the store-wide insertion order of the source event.
	GlobalSeq int64
	// AggregateType names the aggregate the event belongs to.
	AggregateType string
	// AggregateID identifies the aggregate instance.
	AggregateID string
	// AggregateVersion is the event's position in its aggregate stream.
	AggregateVersion int64
	// EventType names the domain event.
	EventType string
	// Payload is the JSON-encoded domain event body.
	Payload []byte
	// Metadata is the JSON-encoded envelope metadata, including actor_id.
	Metadata []byte
	// OccurredAt is when the command produced the event.
	OccurredAt time.Time
	// Attempts counts deliveries including this one, starting at 1.
	Attempts int
	// Replay marks a message enqueued by a backfill rather than a live command.
	// Handlers must suppress user-visible side effects such as emails when set.
	Replay bool
}

// Handler applies messages to one read model.
type Handler interface {
	// Name is the durable subscription name and checkpoint key. It must be
	// stable across releases; changing it replays the whole stream.
	Name() string
	// AggregateTypes limits delivery to the named aggregate types. Returning
	// nil or an empty slice subscribes to every type.
	AggregateTypes() []string
	// Handle applies the message. The context carries the store transaction
	// that also records the delivery, so a handler returning nil is committed
	// exactly once together with its outcome.
	Handle(ctx context.Context, message Message) error
}

// Store is the durable delivery queue an engine drives.
type Store interface {
	// ProcessNext claims the next deliverable message for subscription and runs
	// apply inside the same transaction that records the outcome, reporting
	// Idle when nothing is available.
	//
	// Implementations must guarantee that a message is never delivered while an
	// earlier unprocessed message for the same aggregate is still outstanding,
	// so handlers observe each aggregate's events in order.
	ProcessNext(ctx context.Context, subscription string, aggregateTypes []string, policy Policy, workerID string, apply func(context.Context, Message) error) (Result, error)
	// Lag reports how many messages are still pending for subscription.
	Lag(ctx context.Context, subscription string) (int64, error)
}

// Policy controls retry and dead-lettering for one subscription.
type Policy struct {
	// MaxAttempts dead-letters a message once this many deliveries have failed.
	MaxAttempts int
	// BaseBackoff is the first retry delay; each further attempt doubles it.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential delay.
	MaxBackoff time.Duration
	// LeaseTTL reclaims messages left locked by a crashed worker.
	LeaseTTL time.Duration
}

// DefaultPolicy returns eight attempts, a one-second base backoff capped at
// five minutes, and a one-minute lease.
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 8, BaseBackoff: time.Second, MaxBackoff: 5 * time.Minute, LeaseTTL: time.Minute}
}

// RetryDelay returns the backoff to apply after attempts failed deliveries.
func (p Policy) RetryDelay(attempts int) time.Duration {
	base, maximum := p.BaseBackoff, p.MaxBackoff
	if base <= 0 {
		base = time.Second
	}
	if maximum <= 0 {
		maximum = 5 * time.Minute
	}
	delay := base
	for i := 1; i < attempts && delay < maximum; i++ {
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	return delay
}

// normalise fills zero fields with the defaults so callers may pass Policy{}.
func (p Policy) normalise() Policy {
	defaults := DefaultPolicy()
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaults.MaxAttempts
	}
	if p.BaseBackoff <= 0 {
		p.BaseBackoff = defaults.BaseBackoff
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = defaults.MaxBackoff
	}
	if p.LeaseTTL <= 0 {
		p.LeaseTTL = defaults.LeaseTTL
	}
	return p
}

// Status enumerates the outcomes of processing one message.
type Status int

const (
	// Idle means no message was available to process.
	Idle Status = iota
	// Processed means the handler committed successfully.
	Processed
	// Retried means the handler failed and the message will be redelivered.
	Retried
	// DeadLettered means the handler failed for the last permitted attempt.
	DeadLettered
)

// String renders the status for logs and metrics.
func (s Status) String() string {
	switch s {
	case Processed:
		return "processed"
	case Retried:
		return "retried"
	case DeadLettered:
		return "dead_lettered"
	default:
		return "idle"
	}
}

// Result reports what processing one message did.
type Result struct {
	// Status is the outcome.
	Status Status
	// Message is the claimed message; zero when Status is Idle.
	Message Message
	// Err is the handler failure that caused Retried or DeadLettered.
	Err error
}

// ErrNoStore is returned when an engine is built without a store.
var ErrNoStore = errors.New("outbox store is required")

// ErrNoHandler is returned when an engine is built without a handler.
var ErrNoHandler = errors.New("outbox handler is required")
