//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// appendEvent commits one event through the store's own unit of work.
func appendEvent(t *testing.T, pool *pgxpool.Pool, aggregateType, aggregateID, eventType, actorID string, payload any) {
	t.Helper()
	runner := pgxtx.NewRunner(pool)
	events := postgresadapter.NewEventStore(pool)
	if err := runner.Within(context.Background(), func(ctx context.Context) error {
		return events.Append(ctx, outbound.DomainEvent{
			ID: uuid.NewString(), AggregateType: aggregateType, AggregateID: aggregateID,
			EventType: eventType, Payload: payload, OccurredAt: time.Now().UnixMilli(), ActorID: actorID,
		})
	}); err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
}

// collectingHandler records what it was given and can be told to fail.
type collectingHandler struct {
	mu             sync.Mutex
	name           string
	aggregateTypes []string
	seen           []string
	failWith       error
}

func (h *collectingHandler) Name() string             { return h.name }
func (h *collectingHandler) AggregateTypes() []string { return h.aggregateTypes }

func (h *collectingHandler) Handle(_ context.Context, message outbox.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failWith != nil {
		return h.failWith
	}
	h.seen = append(h.seen, message.EventType)
	return nil
}

func (h *collectingHandler) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func TestOutboxStoreDeliversPerAggregateInOrder(t *testing.T) {
	pool := newPool(t)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	handler := &collectingHandler{name: "ordering"}
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: outbox.DefaultPolicy()})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	aggregateID := uuid.NewString()
	for _, eventType := range []string{"expense_added", "expense_updated", "expense_removed"} {
		appendEvent(t, pool, "expense", aggregateID, eventType, owner, map[string]string{"expense_id": aggregateID})
	}
	drainPostgresEngine(t, engine, 10)

	got := handler.events()
	want := []string{"expense_added", "expense_updated", "expense_removed"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("delivered %v, want %v", got, want)
	}
}

func TestOutboxStoreRetriesThenDeadLetters(t *testing.T) {
	pool := newPool(t)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	handler := &collectingHandler{name: "failing", failWith: errors.New("projection exploded")}
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	// A zero backoff keeps the test fast while still exercising available_at.
	policy := outbox.Policy{MaxAttempts: 3, BaseBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond, LeaseTTL: time.Minute}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: policy})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	aggregateID := uuid.NewString()
	appendEvent(t, pool, "expense", aggregateID, "expense_added", owner, map[string]string{"expense_id": aggregateID})

	for _, want := range []outbox.Status{outbox.Retried, outbox.Retried, outbox.DeadLettered} {
		result, err := engine.Step(context.Background())
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if result.Status != want {
			t.Fatalf("status = %s, want %s", result.Status, want)
		}
	}

	var status, lastError string
	var attempts int
	if err := pool.QueryRow(context.Background(), `select status, coalesce(last_error, ''), attempts from events.outbox where subscription = $1`, handler.Name()).Scan(&status, &lastError, &attempts); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	if status != "dead" || attempts != 3 || lastError == "" {
		t.Fatalf("outbox row = status %q attempts %d error %q", status, attempts, lastError)
	}

	// A dead-lettered message is no longer pending, so it stops counting as lag.
	if lag, err := store.Lag(context.Background(), handler.Name()); err != nil || lag != 0 {
		t.Fatalf("lag = %d, %v; want 0", lag, err)
	}
	letters, err := store.DeadLetters(context.Background(), handler.Name(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(letters) != 1 || letters[0].EventType != "expense_added" {
		t.Fatalf("dead letters = %#v", letters)
	}
}

func TestOutboxStoreBlocksSuccessorsOfADeadLetter(t *testing.T) {
	pool := newPool(t)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	handler := &collectingHandler{name: "blocking", failWith: errors.New("poison")}
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	policy := outbox.Policy{MaxAttempts: 1, BaseBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond, LeaseTTL: time.Minute}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: policy})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	poisoned, healthy := uuid.NewString(), uuid.NewString()
	appendEvent(t, pool, "expense", poisoned, "expense_added", owner, map[string]string{"expense_id": poisoned})
	appendEvent(t, pool, "expense", poisoned, "expense_updated", owner, map[string]string{"expense_id": poisoned})
	appendEvent(t, pool, "expense", healthy, "expense_added", owner, map[string]string{"expense_id": healthy})

	result, err := engine.Step(context.Background())
	if err != nil || result.Status != outbox.DeadLettered {
		t.Fatalf("first step = %s, %v; want dead-lettered", result.Status, err)
	}

	// One aggregate freezing must not stop the rest of the stream.
	handler.mu.Lock()
	handler.failWith = nil
	handler.mu.Unlock()
	drainPostgresEngine(t, engine, 10)

	if got := handler.events(); len(got) != 1 {
		t.Fatalf("delivered %v, want only the unrelated aggregate's event", got)
	}
	// The poisoned aggregate's second event stays pending behind its sibling.
	var pending int
	if err := pool.QueryRow(context.Background(), `select count(*) from events.outbox where subscription = $1 and status = 'pending'`, handler.Name()).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want the successor held behind the dead letter", pending)
	}
}

func TestOutboxStoreConcurrentWorkersDeliverEachMessageOnce(t *testing.T) {
	pool := newPool(t)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	const subscription = "concurrent"
	if err := store.EnsureSubscription(context.Background(), subscription); err != nil {
		t.Fatalf("register subscription: %v", err)
	}

	const messages = 40
	for i := 0; i < messages; i++ {
		aggregateID := uuid.NewString()
		appendEvent(t, pool, "expense", aggregateID, "expense_added", owner, map[string]string{"expense_id": aggregateID})
	}

	var mu sync.Mutex
	delivered := map[string]int{}
	policy := outbox.Policy{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, LeaseTTL: time.Minute}
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for {
				result, err := store.ProcessNext(context.Background(), subscription, nil, policy, fmt.Sprintf("worker-%d", worker), func(_ context.Context, message outbox.Message) error {
					mu.Lock()
					defer mu.Unlock()
					delivered[message.EventID]++
					return nil
				})
				if err != nil {
					t.Errorf("worker %d: %v", worker, err)
					return
				}
				if result.Status == outbox.Idle {
					return
				}
			}
		}(worker)
	}
	workers.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != messages {
		t.Fatalf("delivered %d distinct messages, want %d", len(delivered), messages)
	}
	for eventID, count := range delivered {
		if count != 1 {
			t.Fatalf("event %s delivered %d times; SKIP LOCKED must give each worker a distinct row", eventID, count)
		}
	}
}

func TestOutboxStoreAdvancesTheCheckpoint(t *testing.T) {
	pool := newPool(t)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	handler := &collectingHandler{name: "checkpointing"}
	if err := store.EnsureSubscription(context.Background(), handler.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, handler, outbox.Options{Policy: outbox.DefaultPolicy()})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	for i := 0; i < 3; i++ {
		aggregateID := uuid.NewString()
		appendEvent(t, pool, "expense", aggregateID, "expense_added", owner, map[string]string{"expense_id": aggregateID})
	}
	drainPostgresEngine(t, engine, 10)

	var processed, lastSeq int64
	if err := pool.QueryRow(context.Background(), `select processed_count, last_global_seq from events.projector_checkpoints where subscription = $1`, handler.Name()).Scan(&processed, &lastSeq); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if processed != 3 || lastSeq != 3 {
		t.Fatalf("checkpoint = processed %d last_global_seq %d, want 3/3", processed, lastSeq)
	}
}

func TestAnalyticsProjectionAgainstPostgres(t *testing.T) {
	pool := newPool(t)
	analytics := postgresadapter.NewAnalyticsRepository(pool)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	food := defaultCategoryID(t, pool, "Food")
	transport := defaultCategoryID(t, pool, "Transport")
	tag := seedTag(t, pool, owner, "cinema")

	projection, err := service.NewAnalyticsProjection(analytics)
	if err != nil {
		t.Fatalf("build projection: %v", err)
	}
	if err := store.EnsureSubscription(context.Background(), projection.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, projection, outbox.Options{Policy: outbox.DefaultPolicy()})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	occurred := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	expense := expenseRecord(owner, food, 25_00, occurred, tag)
	appendEvent(t, pool, "expense", expense.ID, "expense_added", owner, domain.ExpenseAdded{Expense: expense})
	drainPostgresEngine(t, engine, 10)

	if amount := monthlyRollup(t, analytics, owner, food, occurred); amount != 25_00 {
		t.Fatalf("rollup after create = %d, want 2500", amount)
	}

	// Recategorise and reprice. The old bucket must be fully reversed.
	expense.CategoryID, expense.AmountMinor = transport, 40_00
	appendEvent(t, pool, "expense", expense.ID, "expense_updated", owner, domain.ExpenseUpdated{Expense: expense})
	drainPostgresEngine(t, engine, 10)

	if amount := monthlyRollup(t, analytics, owner, food, occurred); amount != 0 {
		t.Fatalf("old category rollup = %d, want 0 after the move", amount)
	}
	if amount := monthlyRollup(t, analytics, owner, transport, occurred); amount != 40_00 {
		t.Fatalf("new category rollup = %d, want 4000", amount)
	}

	appendEvent(t, pool, "expense", expense.ID, "expense_removed", owner, domain.ExpenseRemoved{ExpenseID: expense.ID, OwnerID: owner, RemovedAt: time.Now().UTC()})
	drainPostgresEngine(t, engine, 10)
	if amount := monthlyRollup(t, analytics, owner, transport, occurred); amount != 0 {
		t.Fatalf("rollup after removal = %d, want 0", amount)
	}

	// Tag rollups must track the same lifecycle.
	tagRollups, err := analytics.ListExpenseRollups(context.Background(), outbound.AnalyticsFilter{
		OwnerID: owner, Period: domain.AnalyticsMonth,
		From:   domain.RollupPeriodStart(domain.AnalyticsMonth, occurred),
		To:     domain.RollupPeriodStart(domain.AnalyticsMonth, occurred),
		TagIDs: []string{tag},
	})
	if err != nil {
		t.Fatalf("list tag rollups: %v", err)
	}
	for _, rollup := range tagRollups {
		if rollup.AmountMinor != 0 {
			t.Fatalf("tag rollup = %d, want 0 after removal", rollup.AmountMinor)
		}
	}
}

func TestBudgetUsageProjectionAgainstPostgres(t *testing.T) {
	pool := newPool(t)
	usageRepository := postgresadapter.NewBudgetUsageRepository(pool)
	notifications := postgresadapter.NewNotificationRepository(pool)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	food := defaultCategoryID(t, pool, "Food")

	budgetID := uuid.NewString()
	occurred := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `insert into budgets.budgets (id, owner_id, category_id, name, amount_limit_minor, currency, threshold_percent, period) values ($1, $2, $3, 'Cinema', 100_00, 'EUR', 80, 'monthly')`, budgetID, owner, food); err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	projection, err := service.NewBudgetUsageProjection(usageRepository, notifications)
	if err != nil {
		t.Fatalf("build projection: %v", err)
	}
	if err := store.EnsureSubscription(context.Background(), projection.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, projection, outbox.Options{Policy: outbox.DefaultPolicy()})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	first := expenseRecord(owner, food, 50_00, occurred)
	appendEvent(t, pool, "expense", first.ID, "expense_added", owner, domain.ExpenseAdded{Expense: first})
	drainPostgresEngine(t, engine, 10)

	periodStart := domain.RollupPeriodStart(domain.AnalyticsMonth, occurred)
	usage, found, err := usageRepository.LoadUsage(context.Background(), budgetID, periodStart)
	if err != nil || !found {
		t.Fatalf("load usage: found=%v err=%v", found, err)
	}
	if usage.SpentMinor != 50_00 || usage.AlertedPercent != 0 {
		t.Fatalf("usage = %#v, want 5000 spent and no alert", usage)
	}
	if count := countNotifications(t, pool); count != 0 {
		t.Fatalf("queued %d notifications below the threshold, want 0", count)
	}

	// Crossing the threshold must queue exactly one alert, in the same
	// transaction as the usage row.
	second := expenseRecord(owner, food, 40_00, occurred)
	appendEvent(t, pool, "expense", second.ID, "expense_added", owner, domain.ExpenseAdded{Expense: second})
	drainPostgresEngine(t, engine, 10)

	usage, _, err = usageRepository.LoadUsage(context.Background(), budgetID, periodStart)
	if err != nil {
		t.Fatalf("load usage: %v", err)
	}
	if usage.SpentMinor != 90_00 || usage.AlertedPercent != 80 {
		t.Fatalf("usage = %#v, want 9000 spent at the 80 rung", usage)
	}
	if count := countNotifications(t, pool); count != 1 {
		t.Fatalf("queued %d notifications, want exactly 1", count)
	}
}

func TestAuditProjectionAgainstPostgres(t *testing.T) {
	pool := newPool(t)
	audit := postgresadapter.NewAuditRepository(pool)
	store := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")

	projection, err := service.NewAuditProjection(audit)
	if err != nil {
		t.Fatalf("build projection: %v", err)
	}
	if err := store.EnsureSubscription(context.Background(), projection.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	engine, err := outbox.NewEngine(store, projection, outbox.Options{Policy: outbox.DefaultPolicy()})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	aggregateID := uuid.NewString()
	appendEvent(t, pool, "expense", aggregateID, "expense_added", owner, map[string]string{"expense_id": aggregateID})
	appendEvent(t, pool, "budget", uuid.NewString(), "budget_created", owner, map[string]string{"budget_id": uuid.NewString()})
	drainPostgresEngine(t, engine, 10)

	entries, err := audit.ListEntries(context.Background(), outbound.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("recorded %d audit entries, want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.ActorID != owner || entry.EventID == "" || entry.Metadata["global_seq"] == "" {
			t.Fatalf("unexpected audit entry %#v", entry)
		}
	}

	// An audit record must outlive the user it refers to: migration 000007
	// drops the actor foreign key precisely so this delete cannot be blocked.
	if _, err := pool.Exec(context.Background(), `delete from identity.users where id = $1`, owner); err != nil {
		t.Fatalf("delete actor: %v", err)
	}
	remaining, err := audit.ListEntries(context.Background(), outbound.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries after actor removal: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("audit entries did not survive actor deletion: %d remain", len(remaining))
	}
}

func monthlyRollup(t *testing.T, repository *postgresadapter.AnalyticsRepository, ownerID, categoryID string, at time.Time) int64 {
	t.Helper()
	start := domain.RollupPeriodStart(domain.AnalyticsMonth, at)
	rollups, err := repository.ListExpenseRollups(context.Background(), outbound.AnalyticsFilter{
		OwnerID: ownerID, Period: domain.AnalyticsMonth, From: start, To: start, CategoryID: categoryID,
	})
	if err != nil {
		t.Fatalf("list rollups: %v", err)
	}
	var total int64
	for _, rollup := range rollups {
		total += rollup.AmountMinor
	}
	return total
}

func countNotifications(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `select count(*) from notifications.deliveries`).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return count
}

func drainPostgresEngine(t *testing.T, engine *outbox.Engine, limit int) {
	t.Helper()
	for i := 0; i < limit; i++ {
		result, err := engine.Step(context.Background())
		if err != nil {
			t.Fatalf("engine step: %v", err)
		}
		if result.Status == outbox.Idle {
			return
		}
		if result.Status != outbox.Processed {
			t.Fatalf("engine returned %s: %v", result.Status, result.Err)
		}
	}
	t.Fatalf("engine did not drain within %d steps", limit)
}
