package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// projectionHarness wires the in-memory adapters, the real services and the
// real outbox engines together, so a projection test exercises the same code
// path production does rather than a hand-built message.
type projectionHarness struct {
	t             *testing.T
	events        *memory.EventStore
	expenses      *service.ExpenseService
	budgets       *service.BudgetService
	analytics     *memory.AnalyticsRepository
	budgetUsage   *memory.BudgetUsageRepository
	audit         *memory.AuditRepository
	notifications *memory.NotificationRepository
	engines       []*outbox.Engine
}

func newProjectionHarness(t *testing.T) *projectionHarness {
	t.Helper()
	expenseRepository := memory.NewExpenseRepository()
	budgetRepository := memory.NewBudgetRepository()
	groupRepository := memory.NewGroupRepository()
	analytics := memory.NewAnalyticsRepository()
	budgetUsage := memory.NewBudgetUsageRepository(budgetRepository)
	audit := memory.NewAuditRepository()
	notifications := memory.NewNotificationRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenseRepository, budgetRepository, analytics, budgetUsage, audit, notifications, events)
	events.WithUnitOfWork(unit)

	expenses, err := service.NewExpenseService(expenseRepository, events, unit)
	if err != nil {
		t.Fatalf("build expense service: %v", err)
	}
	budgets, err := service.NewBudgetService(budgetRepository, events, groupRepository, unit)
	if err != nil {
		t.Fatalf("build budget service: %v", err)
	}

	harness := &projectionHarness{
		t: t, events: events, expenses: expenses, budgets: budgets,
		analytics: analytics, budgetUsage: budgetUsage, audit: audit, notifications: notifications,
	}

	analyticsProjection, err := service.NewAnalyticsProjection(analytics)
	if err != nil {
		t.Fatalf("build analytics projection: %v", err)
	}
	budgetProjection, err := service.NewBudgetUsageProjection(budgetUsage, notifications)
	if err != nil {
		t.Fatalf("build budget usage projection: %v", err)
	}
	auditProjection, err := service.NewAuditProjection(audit)
	if err != nil {
		t.Fatalf("build audit projection: %v", err)
	}
	for _, projection := range []outbox.Handler{analyticsProjection, budgetProjection, auditProjection} {
		if err := events.EnsureSubscription(context.Background(), projection.Name()); err != nil {
			t.Fatalf("register subscription %s: %v", projection.Name(), err)
		}
		engine, err := outbox.NewEngine(events, projection, outbox.Options{Policy: outbox.DefaultPolicy()})
		if err != nil {
			t.Fatalf("build engine %s: %v", projection.Name(), err)
		}
		harness.engines = append(harness.engines, engine)
	}
	return harness
}

// drain processes every available message on every subscription.
func (h *projectionHarness) drain() {
	h.t.Helper()
	for _, engine := range h.engines {
		for i := 0; i < 200; i++ {
			result, err := engine.Step(context.Background())
			if err != nil {
				h.t.Fatalf("engine %s step: %v", engine.Name(), err)
			}
			if result.Status == outbox.Idle {
				break
			}
			if result.Status != outbox.Processed {
				h.t.Fatalf("engine %s returned %s: %v", engine.Name(), result.Status, result.Err)
			}
		}
	}
}

// rollup returns the monthly category rollup amount for one owner.
func (h *projectionHarness) rollup(ownerID, categoryID string, at time.Time) (int64, int64) {
	h.t.Helper()
	start := domain.RollupPeriodStart(domain.AnalyticsMonth, at)
	values, err := h.analytics.ListExpenseRollups(context.Background(), outbound.AnalyticsFilter{
		OwnerID: ownerID, Period: domain.AnalyticsMonth, From: start, To: start, CategoryID: categoryID,
	})
	if err != nil {
		h.t.Fatalf("list rollups: %v", err)
	}
	var amount, count int64
	for _, value := range values {
		amount += value.AmountMinor
		count += value.ExpenseCount
	}
	return amount, count
}

// usageFor returns the spend recorded against a budget for the period
// containing at. Creating a budget also seeds a zero row for the current
// period, so tests must select the period they mean rather than assume one row.
func (h *projectionHarness) usageFor(budgetID string, at time.Time) int64 {
	h.t.Helper()
	start := domain.RollupPeriodStart(domain.AnalyticsMonth, at)
	for _, usage := range h.budgetUsage.Usage() {
		if usage.BudgetID == budgetID && usage.PeriodStart.Equal(start) {
			return usage.SpentMinor
		}
	}
	return 0
}

func expenseCommand(categoryID string, amountMinor int64, occurredAt time.Time, tagIDs ...string) service.CreateExpenseCommand {
	return service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: categoryID, AmountMinor: amountMinor,
		OccurredAt: occurredAt, TagIDs: tagIDs, Status: domain.ExpenseConfirmed,
	}
}

func TestAnalyticsProjectionAppliesAndReversesContributions(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	occurred := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)

	expense, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 25_00, occurred, "tag-1"))
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	if amount, count := harness.rollup("owner-1", "category-1", occurred); amount != 25_00 || count != 1 {
		t.Fatalf("after create: amount=%d count=%d, want 2500/1", amount, count)
	}

	// Move the expense to another category and change its amount. The old
	// category's bucket must be fully reversed, not merely reduced.
	update := expenseCommand("category-2", 40_00, occurred, "tag-1")
	if _, err := harness.expenses.UpdateExpense(context.Background(), "owner-1", expense.ID, update); err != nil {
		t.Fatalf("update expense: %v", err)
	}
	harness.drain()
	if amount, count := harness.rollup("owner-1", "category-1", occurred); amount != 0 || count != 0 {
		t.Fatalf("old category not reversed: amount=%d count=%d", amount, count)
	}
	if amount, count := harness.rollup("owner-1", "category-2", occurred); amount != 40_00 || count != 1 {
		t.Fatalf("new category not applied: amount=%d count=%d", amount, count)
	}

	if err := harness.expenses.DeleteExpense(context.Background(), "owner-1", expense.ID); err != nil {
		t.Fatalf("delete expense: %v", err)
	}
	harness.drain()
	if amount, count := harness.rollup("owner-1", "category-2", occurred); amount != 0 || count != 0 {
		t.Fatalf("delete not reversed: amount=%d count=%d", amount, count)
	}
}

func TestAnalyticsProjectionIsIdempotentAcrossRedelivery(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	occurred := time.Date(2026, time.April, 4, 9, 0, 0, 0, time.UTC)
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 12_50, occurred)); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	// Draining again must not double-count: every message is already processed.
	harness.drain()
	if amount, count := harness.rollup("owner-1", "category-1", occurred); amount != 12_50 || count != 1 {
		t.Fatalf("redelivery changed the rollup: amount=%d count=%d", amount, count)
	}
}

func TestAnalyticsProjectionRollsUpEveryPeriod(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	occurred := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 30_00, occurred)); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	for _, period := range domain.RollupPeriods() {
		start := domain.RollupPeriodStart(period, occurred)
		values, err := harness.analytics.ListExpenseRollups(context.Background(), outbound.AnalyticsFilter{
			OwnerID: "owner-1", Period: period, From: start, To: start,
		})
		if err != nil {
			t.Fatalf("list %s rollups: %v", period, err)
		}
		if len(values) != 1 || values[0].AmountMinor != 30_00 {
			t.Fatalf("%s rollup = %#v, want one bucket of 3000", period, values)
		}
	}
}

func TestBudgetUsageProjectionTracksSpendAndAlertsOnce(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	owner := domain.AppUser{ID: "owner-1", Role: domain.RoleMember}
	occurred := time.Date(2026, time.May, 12, 10, 0, 0, 0, time.UTC)

	budget, err := harness.budgets.CreateBudget(context.Background(), owner, domain.BudgetRecord{
		Name: "Cinema", CategoryID: "category-1", AmountLimitMinor: 100_00, Currency: "EUR",
		ThresholdPercent: 80, Period: domain.BudgetMonthly,
	})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	harness.drain()

	// Below the threshold: usage is tracked, nothing is queued.
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 50_00, occurred)); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	if spent := harness.usageFor(budget.ID, occurred); spent != 50_00 {
		t.Fatalf("usage after first expense = %d, want 5000", spent)
	}
	if queued := budgetAlerts(harness.notifications.Deliveries()); len(queued) != 0 {
		t.Fatalf("no alert expected below the threshold, got %#v", queued)
	}

	// Crossing 80% queues exactly one alert.
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 35_00, occurred)); err != nil {
		t.Fatalf("create second expense: %v", err)
	}
	harness.drain()
	queued := budgetAlerts(harness.notifications.Deliveries())
	if len(queued) != 1 {
		t.Fatalf("expected one budget alert, got %d", len(queued))
	}
	if queued[0].UserID != "owner-1" || queued[0].Payload["budget_id"] != budget.ID {
		t.Fatalf("unexpected alert payload %#v", queued[0])
	}

	// More spend at the same rung stays quiet.
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 2_00, occurred)); err != nil {
		t.Fatalf("create third expense: %v", err)
	}
	harness.drain()
	if queued := budgetAlerts(harness.notifications.Deliveries()); len(queued) != 1 {
		t.Fatalf("expected no repeat alert at the same rung, got %d", len(queued))
	}

	// Passing the limit is a separate rung and alerts again.
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 30_00, occurred)); err != nil {
		t.Fatalf("create fourth expense: %v", err)
	}
	harness.drain()
	if queued := budgetAlerts(harness.notifications.Deliveries()); len(queued) != 2 {
		t.Fatalf("expected an exceeded alert, got %d alerts", len(queued))
	}
}

func TestBudgetUsageProjectionRecomputesWhenAnExpenseMovesPeriod(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	owner := domain.AppUser{ID: "owner-1", Role: domain.RoleMember}
	if _, err := harness.budgets.CreateBudget(context.Background(), owner, domain.BudgetRecord{
		Name: "Cinema", CategoryID: "category-1", AmountLimitMinor: 100_00, Currency: "EUR",
		ThresholdPercent: 90, Period: domain.BudgetMonthly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	march := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	april := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)

	expense, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 20_00, march))
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()

	// Correcting the date must empty March and fill April, not leave both set.
	if _, err := harness.expenses.UpdateExpense(context.Background(), "owner-1", expense.ID, expenseCommand("category-1", 20_00, april)); err != nil {
		t.Fatalf("update expense: %v", err)
	}
	harness.drain()

	byPeriod := map[time.Time]int64{}
	for _, usage := range harness.budgetUsage.Usage() {
		byPeriod[usage.PeriodStart] = usage.SpentMinor
	}
	if got := byPeriod[domain.RollupPeriodStart(domain.AnalyticsMonth, march)]; got != 0 {
		t.Fatalf("March usage = %d, want 0", got)
	}
	if got := byPeriod[domain.RollupPeriodStart(domain.AnalyticsMonth, april)]; got != 20_00 {
		t.Fatalf("April usage = %d, want 2000", got)
	}
}

func TestBudgetUsageProjectionDropsUsageWhenBudgetIsRemoved(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	owner := domain.AppUser{ID: "owner-1", Role: domain.RoleMember}
	budget, err := harness.budgets.CreateBudget(context.Background(), owner, domain.BudgetRecord{
		Name: "Cinema", CategoryID: "category-1", AmountLimitMinor: 100_00, Currency: "EUR",
		ThresholdPercent: 80, Period: domain.BudgetMonthly,
	})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 50_00, time.Now().UTC())); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	if len(harness.budgetUsage.Usage()) == 0 {
		t.Fatal("expected usage before removal")
	}

	if err := harness.budgets.DeleteBudget(context.Background(), owner, budget.ID); err != nil {
		t.Fatalf("delete budget: %v", err)
	}
	harness.drain()
	if usage := harness.budgetUsage.Usage(); len(usage) != 0 {
		t.Fatalf("usage survived budget removal: %#v", usage)
	}
}

func TestAuditProjectionRecordsEveryAggregate(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	owner := domain.AppUser{ID: "owner-1", Role: domain.RoleMember}
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 10_00, time.Now().UTC())); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := harness.budgets.CreateBudget(context.Background(), owner, domain.BudgetRecord{
		Name: "Cinema", CategoryID: "category-1", AmountLimitMinor: 100_00, Currency: "EUR",
		ThresholdPercent: 80, Period: domain.BudgetMonthly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	harness.drain()

	entries, err := harness.audit.ListEntries(context.Background(), outbound.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	actions := map[string]domain.AuditEntry{}
	for _, entry := range entries {
		actions[entry.Action] = entry
	}
	if _, ok := actions["expense_added"]; !ok {
		t.Fatalf("expense_added not audited, got %#v", actions)
	}
	budgetEntry, ok := actions["budget_created"]
	if !ok {
		t.Fatalf("budget_created not audited, got %#v", actions)
	}
	if budgetEntry.ActorID != "owner-1" || budgetEntry.ResourceType != "budget" {
		t.Fatalf("unexpected audit entry %#v", budgetEntry)
	}
}

func TestAuditProjectionIgnoresRedeliveredEvents(t *testing.T) {
	t.Parallel()
	harness := newProjectionHarness(t)
	if _, err := harness.expenses.CreateExpense(context.Background(), "owner-1", expenseCommand("category-1", 10_00, time.Now().UTC())); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	harness.drain()
	before, err := harness.audit.ListEntries(context.Background(), outbound.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	harness.drain()
	after, err := harness.audit.ListEntries(context.Background(), outbound.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("redelivery duplicated audit entries: %d then %d", len(before), len(after))
	}
}

func budgetAlerts(deliveries []domain.NotificationDelivery) []domain.NotificationDelivery {
	alerts := make([]domain.NotificationDelivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Kind == domain.NotificationBudgetAlert {
			alerts = append(alerts, delivery)
		}
	}
	return alerts
}
