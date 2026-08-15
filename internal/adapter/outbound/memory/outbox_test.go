package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// recordingHandler applies messages to a slice and can be told to fail.
type recordingHandler struct {
	mu             sync.Mutex
	name           string
	aggregateTypes []string
	handled        []outbox.Message
	failWith       error
}

func (h *recordingHandler) Name() string             { return h.name }
func (h *recordingHandler) AggregateTypes() []string { return h.aggregateTypes }

func (h *recordingHandler) Handle(_ context.Context, message outbox.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failWith != nil {
		return h.failWith
	}
	h.handled = append(h.handled, message)
	return nil
}

func (h *recordingHandler) handledTypes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	types := make([]string, 0, len(h.handled))
	for _, message := range h.handled {
		types = append(types, message.EventType)
	}
	return types
}

func (h *recordingHandler) setFailure(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failWith = err
}

// testClock is a manually advanced clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func event(aggregateType, aggregateID, eventType string) outbound.DomainEvent {
	return outbound.DomainEvent{
		ID: uuid.NewString(), AggregateType: aggregateType, AggregateID: aggregateID,
		EventType: eventType, Payload: map[string]string{"kind": eventType},
		OccurredAt: time.Now().UnixMilli(), ActorID: "owner-1",
	}
}

func newEngine(t *testing.T, store *memory.EventStore, handler outbox.Handler, policy outbox.Policy) *outbox.Engine {
	t.Helper()
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: policy})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	return engine
}

func TestEngineDeliversInGlobalOrder(t *testing.T) {
	t.Parallel()
	store := memory.NewEventStore()
	handler := &recordingHandler{name: "ordering"}
	engine := newEngine(t, store, handler, outbox.DefaultPolicy())

	for _, eventType := range []string{"expense_added", "expense_updated", "expense_removed"} {
		if err := store.Append(context.Background(), event("expense", "expense-1", eventType)); err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
	}
	drainEngine(t, engine, 10)

	got := handler.handledTypes()
	want := []string{"expense_added", "expense_updated", "expense_removed"}
	if len(got) != len(want) {
		t.Fatalf("handled %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handled %v, want %v", got, want)
		}
	}
}

func TestEngineFiltersByAggregateType(t *testing.T) {
	t.Parallel()
	store := memory.NewEventStore()
	handler := &recordingHandler{name: "expenses-only", aggregateTypes: []string{"expense"}}
	engine := newEngine(t, store, handler, outbox.DefaultPolicy())

	if err := store.Append(context.Background(), event("budget", "budget-1", "budget_created")); err != nil {
		t.Fatalf("append budget event: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append expense event: %v", err)
	}
	drainEngine(t, engine, 10)

	if got := handler.handledTypes(); len(got) != 1 || got[0] != "expense_added" {
		t.Fatalf("handled %v, want only expense_added", got)
	}
}

func TestEngineRetriesWithBackoffThenDeadLetters(t *testing.T) {
	t.Parallel()
	clock := &testClock{now: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)}
	store := memory.NewEventStore().WithClock(clock.Now)
	handler := &recordingHandler{name: "failing", failWith: errors.New("projection exploded")}
	policy := outbox.Policy{MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute, LeaseTTL: time.Minute}
	engine := newEngine(t, store, handler, policy)

	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append event: %v", err)
	}

	// Attempts one and two retry; the third exhausts the policy and dead-letters.
	for _, want := range []outbox.Status{outbox.Retried, outbox.Retried, outbox.DeadLettered} {
		result, err := engine.Step(context.Background())
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if result.Status != want {
			t.Fatalf("step status = %s, want %s", result.Status, want)
		}
		// Backoff must actually hold the message back until time passes.
		if result.Status == outbox.Retried {
			idle, err := engine.Step(context.Background())
			if err != nil {
				t.Fatalf("step during backoff: %v", err)
			}
			if idle.Status != outbox.Idle {
				t.Fatalf("message redelivered during backoff, status %s", idle.Status)
			}
			clock.advance(time.Minute)
		}
	}

	stats := engine.Stats()
	if stats.Retried != 2 || stats.DeadLettered != 1 || stats.Processed != 0 {
		t.Fatalf("unexpected stats %#v", stats)
	}
	if stats.LastError == "" {
		t.Fatal("expected the failure cause to be recorded")
	}
}

func TestEngineBlocksLaterEventsOfADeadLetteredAggregate(t *testing.T) {
	t.Parallel()
	clock := &testClock{now: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)}
	store := memory.NewEventStore().WithClock(clock.Now)
	handler := &recordingHandler{name: "blocking", failWith: errors.New("poison")}
	policy := outbox.Policy{MaxAttempts: 1, BaseBackoff: time.Second, MaxBackoff: time.Minute, LeaseTTL: time.Minute}
	engine := newEngine(t, store, handler, policy)

	// Two events for the poisoned aggregate, one for an unrelated aggregate.
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_updated")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-2", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}

	result, err := engine.Step(context.Background())
	if err != nil || result.Status != outbox.DeadLettered {
		t.Fatalf("first step = %s, %v; want dead-lettered", result.Status, err)
	}

	// The unrelated aggregate must still flow while the poisoned one is frozen.
	handler.setFailure(nil)
	clock.advance(time.Minute)
	drainEngine(t, engine, 10)

	handled := handler.handledTypes()
	if len(handled) != 1 {
		t.Fatalf("handled %v, want only the unrelated aggregate's event", handled)
	}
	lag, err := store.Lag(context.Background(), handler.Name())
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	// expense-1's second event is still parked behind its dead-lettered
	// sibling — that is the ordering guarantee asserted above — but it is not
	// lag, because no amount of healthy processing will ever deliver it.
	// Counting it made every lag-based alarm fire forever after one poison
	// event.
	if lag != 0 {
		t.Fatalf("lag = %d, want 0: a message parked behind a dead letter is not deliverable work", lag)
	}
}

func TestEngineReclaimsAnExpiredLease(t *testing.T) {
	t.Parallel()
	clock := &testClock{now: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)}
	store := memory.NewEventStore().WithClock(clock.Now)
	policy := outbox.Policy{MaxAttempts: 5, BaseBackoff: time.Second, MaxBackoff: time.Minute, LeaseTTL: 30 * time.Second}
	if err := store.EnsureSubscription(context.Background(), "leases"); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Simulate a worker that claims a message and then dies mid-handler.
	crashed := errors.New("worker crashed")
	if _, err := store.ProcessNext(context.Background(), "leases", nil, policy, "worker-a", func(context.Context, outbox.Message) error {
		return crashed
	}); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	clock.advance(time.Minute)
	handler := &recordingHandler{name: "leases"}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: policy})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	result, err := engine.Step(context.Background())
	if err != nil {
		t.Fatalf("step after lease expiry: %v", err)
	}
	if result.Status != outbox.Processed {
		t.Fatalf("status = %s, want processed after the lease expired", result.Status)
	}
	// The crashed attempt still counts, so a poison message cannot loop forever.
	if result.Message.Attempts != 2 {
		t.Fatalf("attempts = %d, want the crashed attempt to count", result.Message.Attempts)
	}
}

func TestEngineReportsLagAndHealth(t *testing.T) {
	t.Parallel()
	store := memory.NewEventStore()
	handler := &recordingHandler{name: "health"}
	engine := newEngine(t, store, handler, outbox.DefaultPolicy())

	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}
	check := engine.Health(time.Hour)
	// A backlog that has not yet had time to drain is healthy.
	if err := check(context.Background()); err != nil {
		t.Fatalf("fresh backlog reported unhealthy: %v", err)
	}
	drainEngine(t, engine, 10)
	if err := check(context.Background()); err != nil {
		t.Fatalf("drained subscription reported unhealthy: %v", err)
	}
	if lag, err := store.Lag(context.Background(), handler.Name()); err != nil || lag != 0 {
		t.Fatalf("lag = %d, %v; want 0", lag, err)
	}
}

// TestEngineHealthRecoversAfterDeadLetter guards against a readiness check
// that stays unhealthy forever once a single message is dead-lettered.
// DeadLettered removes the message from the pending count (Lag only counts
// what is still outstanding), so a subscription making progress on
// everything after a poison message is not stuck and must not permanently
// fail readiness over it — that would mean a pod can never become Ready
// again without a restart, long after the one bad message stopped mattering.
// The count itself stays visible via Stats().DeadLettered for alerting.
func TestEngineHealthRecoversAfterDeadLetter(t *testing.T) {
	t.Parallel()
	store := memory.NewEventStore()
	handler := &recordingHandler{name: "dead", failWith: errors.New("poison")}
	engine := newEngine(t, store, handler, outbox.Policy{MaxAttempts: 1, BaseBackoff: time.Second, MaxBackoff: time.Second, LeaseTTL: time.Minute})
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := engine.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	// The dead-lettered message has nothing queued behind it, so the backlog
	// is drained and readiness must not stay permanently unready over it.
	if err := engine.Health(time.Hour)(context.Background()); err != nil {
		t.Fatalf("a lone dead-lettered message with an empty backlog must not fail readiness: %v", err)
	}
}

// TestEngineHealthRecoversWhileADeadLetterBlocksItsAggregate reproduces the
// production outage this behaviour caused. A poisoned expense event
// dead-letters, which permanently blocks the later events for that same
// expense. Those events stay 'pending' in the database forever, so a lag-based
// staleness check failed from then on — every readiness probe returned 503,
// Kubernetes pulled the only replica out of the Service, and because the dead
// row lives in the database no restart could clear it. The pod stayed up,
// healthy, and unreachable.
//
// A message that can never be delivered is not a backlog, so it must not hold
// the subscription "stalled" forever.
func TestEngineHealthRecoversWhileADeadLetterBlocksItsAggregate(t *testing.T) {
	t.Parallel()
	clock := &testClock{now: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)}
	store := memory.NewEventStore().WithClock(clock.Now)
	handler := &recordingHandler{name: "blocking", failWith: errors.New("poison")}
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	policy := outbox.Policy{MaxAttempts: 1, BaseBackoff: time.Second, MaxBackoff: time.Second, LeaseTTL: time.Minute}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: policy, Clock: clock.Now})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_added")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Append(context.Background(), event("expense", "expense-1", "expense_updated")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := engine.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	// Well past any staleness window: the sibling is still parked behind the
	// dead letter and always will be, so this must report healthy rather than
	// stalling permanently.
	clock.advance(2 * time.Hour)
	if err := engine.Health(time.Hour)(context.Background()); err != nil {
		t.Fatalf("an event permanently parked behind a dead letter must not stall the subscription forever: %v", err)
	}
}

func TestEnsureSubscriptionBackfillsExistingHistory(t *testing.T) {
	t.Parallel()
	store := memory.NewEventStore()
	// Events appended before any subscription exists must still reach a
	// subscription registered later; that is how a new projection backfills.
	for _, eventType := range []string{"expense_added", "expense_updated"} {
		if err := store.Append(context.Background(), event("expense", "expense-1", eventType)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	handler := &recordingHandler{name: "late-joiner"}
	engine := newEngine(t, store, handler, outbox.DefaultPolicy())
	drainEngine(t, engine, 10)

	if got := handler.handledTypes(); len(got) != 2 {
		t.Fatalf("handled %v, want both historical events", got)
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for _, message := range handler.handled {
		if !message.Replay {
			t.Fatalf("backfilled message %s must be marked as a replay", message.EventType)
		}
	}
}

func drainEngine(t *testing.T, engine *outbox.Engine, limit int) {
	t.Helper()
	for i := 0; i < limit; i++ {
		result, err := engine.Step(context.Background())
		if err != nil {
			t.Fatalf("engine step: %v", err)
		}
		if result.Status == outbox.Idle {
			return
		}
	}
	t.Fatalf("engine did not drain within %d steps", limit)
}
