package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// deliveryStatus mirrors the PostgreSQL outbox lifecycle.
type deliveryStatus string

const (
	deliveryPending   deliveryStatus = "pending"
	deliveryProcessed deliveryStatus = "processed"
	deliveryDead      deliveryStatus = "dead"
)

// delivery is one message queued for one subscription.
type delivery struct {
	subscription string
	event        *storedEvent
	status       deliveryStatus
	attempts     int
	availableAt  time.Time
	lockedAt     time.Time
	replay       bool
	lastError    string
}

// storedEvent is an appended event with its assigned global order.
type storedEvent struct {
	event     outbound.DomainEvent
	globalSeq int64
	version   int64
	payload   []byte
	metadata  []byte
}

// EventStore records events and drives outbox subscriptions in memory.
//
// It implements both the event-store port and the outbox store contract, so
// projections run end to end in tests and in local development without
// PostgreSQL. Ordering, leasing, retry backoff and dead-lettering match the
// PostgreSQL adapter's semantics closely enough that a projection correct
// against one is correct against the other.
type EventStore struct {
	mu            sync.Mutex
	events        []*storedEvent
	deliveries    []*delivery
	subscriptions map[string]struct{}
	versions      map[string]int64
	sequence      int64
	unit          outbound.UnitOfWork
	now           func() time.Time
}

// NewEventStore creates an empty append-only event log.
func NewEventStore() *EventStore {
	return &EventStore{subscriptions: map[string]struct{}{}, versions: map[string]int64{}, now: time.Now}
}

// WithUnitOfWork makes projection handlers run inside a unit of work, so a
// failing handler discards its partial writes exactly as a rolled-back
// PostgreSQL transaction would.
func (s *EventStore) WithUnitOfWork(unit outbound.UnitOfWork) *EventStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unit = unit
	return s
}

// WithClock replaces the clock used for lease expiry and retry backoff, so
// tests can advance time instead of sleeping through it.
func (s *EventStore) WithClock(now func() time.Time) *EventStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
	return s
}

// Append records an event and queues it for every registered subscription.
func (s *EventStore) Append(_ context.Context, event outbound.DomainEvent) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	metadata, err := json.Marshal(map[string]string{"actor_id": event.ActorID})
	if err != nil {
		return fmt.Errorf("marshal event metadata: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	key := event.AggregateType + ":" + event.AggregateID
	s.versions[key]++
	stored := &storedEvent{event: event, globalSeq: s.sequence, version: s.versions[key], payload: payload, metadata: metadata}
	s.events = append(s.events, stored)
	for subscription := range s.subscriptions {
		s.deliveries = append(s.deliveries, &delivery{subscription: subscription, event: stored, status: deliveryPending})
	}
	return nil
}

// EnsureSubscription registers a subscription and, when it is new, queues every
// existing event for it as a replay.
func (s *EventStore) EnsureSubscription(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.subscriptions[name]; exists {
		return nil
	}
	s.subscriptions[name] = struct{}{}
	for _, stored := range s.events {
		s.deliveries = append(s.deliveries, &delivery{subscription: name, event: stored, status: deliveryPending, replay: true})
	}
	return nil
}

// ProcessNext claims the next deliverable message and applies it.
func (s *EventStore) ProcessNext(ctx context.Context, subscription string, aggregateTypes []string, policy outbox.Policy, workerID string, apply func(context.Context, outbox.Message) error) (outbox.Result, error) {
	claimed, message, ok := s.claim(subscription, aggregateTypes, policy, workerID)
	if !ok {
		return outbox.Result{Status: outbox.Idle}, nil
	}
	handle := func(ctx context.Context) error { return apply(ctx, message) }
	var err error
	if s.unit != nil {
		err = s.unit.Within(ctx, handle)
	} else {
		err = handle(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		claimed.status, claimed.lockedAt, claimed.lastError = deliveryProcessed, time.Time{}, ""
		return outbox.Result{Status: outbox.Processed, Message: message}, nil
	}
	claimed.lockedAt, claimed.lastError = time.Time{}, err.Error()
	claimed.availableAt = s.now().Add(policy.RetryDelay(claimed.attempts))
	if claimed.attempts >= policy.MaxAttempts {
		claimed.status = deliveryDead
		return outbox.Result{Status: outbox.DeadLettered, Message: message, Err: err}, nil
	}
	return outbox.Result{Status: outbox.Retried, Message: message, Err: err}, nil
}

// claim locks the next deliverable message, applying the same per-aggregate
// blocker rule as the PostgreSQL store: a message waits while any earlier
// message for its aggregate is still pending or dead-lettered.
func (s *EventStore) claim(subscription string, aggregateTypes []string, policy outbox.Policy, workerID string) (*delivery, outbox.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	lease := policy.LeaseTTL
	if lease <= 0 {
		lease = time.Minute
	}
	candidates := make([]*delivery, 0, len(s.deliveries))
	for _, value := range s.deliveries {
		if value.subscription != subscription || value.status != deliveryPending {
			continue
		}
		if value.availableAt.After(now) {
			continue
		}
		if !value.lockedAt.IsZero() && value.lockedAt.After(now.Add(-lease)) {
			continue
		}
		if !matchesAggregateType(value.event.event.AggregateType, aggregateTypes) {
			continue
		}
		candidates = append(candidates, value)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].event.globalSeq < candidates[j].event.globalSeq })
	for _, candidate := range candidates {
		if s.blockedLocked(candidate) {
			continue
		}
		candidate.attempts++
		candidate.lockedAt = now
		return candidate, s.messageLocked(candidate, workerID), true
	}
	return nil, outbox.Message{}, false
}

// blockedLocked reports whether an earlier message for the same aggregate is
// still outstanding. The caller must hold the mutex.
func (s *EventStore) blockedLocked(candidate *delivery) bool {
	for _, other := range s.deliveries {
		if other == candidate || other.subscription != candidate.subscription {
			continue
		}
		if other.event.event.AggregateType != candidate.event.event.AggregateType {
			continue
		}
		if other.event.event.AggregateID != candidate.event.event.AggregateID {
			continue
		}
		if other.event.globalSeq >= candidate.event.globalSeq {
			continue
		}
		if other.status == deliveryPending || other.status == deliveryDead {
			return true
		}
	}
	return false
}

func (s *EventStore) messageLocked(value *delivery, _ string) outbox.Message {
	stored := value.event
	return outbox.Message{
		OutboxID:         fmt.Sprintf("%s:%d", value.subscription, stored.globalSeq),
		EventID:          stored.event.ID,
		GlobalSeq:        stored.globalSeq,
		AggregateType:    stored.event.AggregateType,
		AggregateID:      stored.event.AggregateID,
		AggregateVersion: stored.version,
		EventType:        stored.event.EventType,
		Payload:          append([]byte(nil), stored.payload...),
		Metadata:         append([]byte(nil), stored.metadata...),
		OccurredAt:       time.UnixMilli(stored.event.OccurredAt).UTC(),
		Attempts:         value.attempts,
		Replay:           value.replay,
	}
}

// Lag reports how many messages are still pending for a subscription.
func (s *EventStore) Lag(_ context.Context, subscription string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending int64
	for _, value := range s.deliveries {
		if value.subscription != subscription || value.status != deliveryPending {
			continue
		}
		// Messages parked behind a dead letter can never be claimed, so they
		// are not lag: counting them reports a backlog no amount of healthy
		// processing can drain. See OutboxStore.Lag for the full reasoning.
		if s.deadBlockedLocked(value) {
			continue
		}
		pending++
	}
	return pending, nil
}

// deadBlockedLocked reports whether an earlier message for the same aggregate
// was dead-lettered, which blocks this one permanently. The caller must hold
// the mutex.
func (s *EventStore) deadBlockedLocked(candidate *delivery) bool {
	for _, other := range s.deliveries {
		if other == candidate || other.subscription != candidate.subscription {
			continue
		}
		if other.event.event.AggregateType != candidate.event.event.AggregateType {
			continue
		}
		if other.event.event.AggregateID != candidate.event.event.AggregateID {
			continue
		}
		if other.event.globalSeq >= candidate.event.globalSeq {
			continue
		}
		if other.status == deliveryDead {
			return true
		}
	}
	return false
}

// Events returns a copy of the appended events to prevent tests mutating the store.
func (s *EventStore) Events() []outbound.DomainEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]outbound.DomainEvent, 0, len(s.events))
	for _, stored := range s.events {
		values = append(values, stored.event)
	}
	return values
}

// Snapshot copies the log and its delivery state and returns a function
// restoring them.
func (s *EventStore) Snapshot() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := append([]*storedEvent(nil), s.events...)
	deliveries := make([]*delivery, 0, len(s.deliveries))
	restores := make([]func(), 0, len(s.deliveries))
	for _, value := range s.deliveries {
		saved := *value
		target := value
		deliveries = append(deliveries, value)
		restores = append(restores, func() { *target = saved })
	}
	versions := make(map[string]int64, len(s.versions))
	for key, version := range s.versions {
		versions[key] = version
	}
	sequence := s.sequence
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.events, s.deliveries, s.versions, s.sequence = events, deliveries, versions, sequence
		for _, restore := range restores {
			restore()
		}
	}
}

func matchesAggregateType(aggregateType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == aggregateType {
			return true
		}
	}
	return false
}
