package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestRollupPeriodStartTruncatesToUTCBuckets(t *testing.T) {
	t.Parallel()
	// A Wednesday, late in the day, in a non-UTC zone.
	at := time.Date(2026, time.March, 18, 23, 45, 0, 0, time.FixedZone("CET", 3600))
	for _, test := range []struct {
		period domain.AnalyticsPeriod
		want   time.Time
	}{
		{domain.AnalyticsDay, time.Date(2026, time.March, 18, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsWeek, time.Date(2026, time.March, 16, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsMonth, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsYear, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(string(test.period), func(t *testing.T) {
			if got := domain.RollupPeriodStart(test.period, at); !got.Equal(test.want) {
				t.Fatalf("RollupPeriodStart(%s) = %s, want %s", test.period, got, test.want)
			}
		})
	}
}

func TestRollupPeriodStartWeekStartsOnMonday(t *testing.T) {
	t.Parallel()
	// Sunday must belong to the week that began the previous Monday, matching
	// PostgreSQL's date_trunc('week', ...).
	sunday := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	want := time.Date(2026, time.March, 16, 0, 0, 0, 0, time.UTC)
	if got := domain.RollupPeriodStart(domain.AnalyticsWeek, sunday); !got.Equal(want) {
		t.Fatalf("week start for Sunday = %s, want %s", got, want)
	}
}

func TestDecodeExpenseContributionIgnoresUnrelatedEvents(t *testing.T) {
	t.Parallel()
	// The projection must be able to skip an event before it reads or reverses
	// any previously applied state.
	_, applies, err := domain.DecodeExpenseContribution("expense_archived", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applies {
		t.Fatal("unknown event type must not apply")
	}
}

func TestDecodeExpenseContributionReadsAddedEvent(t *testing.T) {
	t.Parallel()
	occurred := time.Date(2026, time.February, 2, 10, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(domain.ExpenseAdded{Expense: domain.ExpenseRecord{
		ID: "expense-1", OwnerID: "owner-1", CategoryID: "category-1", Currency: "EUR",
		AmountMinor: 25_00, OccurredAt: occurred, TagIDs: []string{"tag-1", "tag-2"},
	}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	contribution, applies, err := domain.DecodeExpenseContribution("expense_added", payload)
	if err != nil || !applies {
		t.Fatalf("DecodeExpenseContribution = %v, %v", applies, err)
	}
	if contribution.ExpenseID != "expense-1" || contribution.AmountMinor != 25_00 || !contribution.Active {
		t.Fatalf("unexpected contribution %#v", contribution)
	}
	if len(contribution.TagIDs) != 2 || !contribution.OccurredAt.Equal(occurred) {
		t.Fatalf("unexpected contribution %#v", contribution)
	}
}

func TestDecodeExpenseContributionReadsRemovedEvent(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(domain.ExpenseRemoved{ExpenseID: "expense-1", OwnerID: "owner-1", RemovedAt: time.Now()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	contribution, applies, err := domain.DecodeExpenseContribution("expense_removed", payload)
	if err != nil || !applies {
		t.Fatalf("DecodeExpenseContribution = %v, %v", applies, err)
	}
	if contribution.ExpenseID != "expense-1" || contribution.Active {
		t.Fatalf("removal must yield an inactive contribution, got %#v", contribution)
	}
}

func TestDecodeBudgetAcceptsLegacyUntaggedRemoval(t *testing.T) {
	t.Parallel()
	// BudgetRemoved carried no json tags before this change, so stored events
	// spell its fields as Go identifiers. Those events are immutable.
	legacy := []byte(`{"BudgetID":"budget-1","OwnerID":"owner-1","RemovedAt":"2026-01-05T00:00:00Z"}`)
	budget, applies, err := domain.DecodeBudget("budget_removed", legacy)
	if err != nil || !applies {
		t.Fatalf("DecodeBudget = %v, %v", applies, err)
	}
	if budget.ID != "budget-1" || budget.OwnerID != "owner-1" || budget.DeletedAt == nil {
		t.Fatalf("unexpected budget %#v", budget)
	}
}

func TestDecodeBudgetAcceptsTaggedRemoval(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(domain.BudgetRemoved{BudgetID: "budget-2", OwnerID: "owner-1", RemovedAt: time.Now()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	budget, applies, err := domain.DecodeBudget("budget_removed", payload)
	if err != nil || !applies {
		t.Fatalf("DecodeBudget = %v, %v", applies, err)
	}
	if budget.ID != "budget-2" || budget.DeletedAt == nil {
		t.Fatalf("unexpected budget %#v", budget)
	}
}

func TestBudgetWindowByPeriod(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		period      domain.BudgetPeriod
		start, end  time.Time
		description string
	}{
		{domain.BudgetDaily, time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC), "daily"},
		{domain.BudgetWeekly, time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC), "weekly"},
		{domain.BudgetMonthly, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), "monthly"},
		{domain.BudgetYearly, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), "yearly"},
	} {
		t.Run(test.description, func(t *testing.T) {
			start, end := domain.BudgetWindow(domain.BudgetRecord{Period: test.period}, at)
			if !start.Equal(test.start) || !end.Equal(test.end) {
				t.Fatalf("BudgetWindow(%s) = [%s, %s), want [%s, %s)", test.period, start, end, test.start, test.end)
			}
		})
	}
}

func TestBudgetThresholdCrossingFiresOncePerRung(t *testing.T) {
	t.Parallel()
	budget := domain.BudgetRecord{ID: "budget-1", OwnerID: "owner-1", AmountLimitMinor: 100_00, ThresholdPercent: 80}
	usage := domain.BudgetUsage{BudgetID: budget.ID}

	if _, crossed := domain.BudgetThresholdCrossing(budget, usage, 50_00); crossed {
		t.Fatal("spend below the threshold must not alert")
	}

	alert, crossed := domain.BudgetThresholdCrossing(budget, usage, 85_00)
	if !crossed || alert.PercentUsed != 85 || alert.Exceeded {
		t.Fatalf("expected a threshold alert, got %#v crossed=%v", alert, crossed)
	}

	// Record the rung, then confirm further spend at the same rung stays quiet.
	usage.AlertedPercent = domain.AlertedPercentFor(budget, usage, 85_00)
	if _, crossed := domain.BudgetThresholdCrossing(budget, usage, 90_00); crossed {
		t.Fatal("a second alert must not fire at the same rung")
	}

	// Passing the limit is a separate rung and alerts again.
	alert, crossed = domain.BudgetThresholdCrossing(budget, usage, 120_00)
	if !crossed || !alert.Exceeded || alert.PercentUsed != 120 {
		t.Fatalf("expected an exceeded alert, got %#v crossed=%v", alert, crossed)
	}
}

func TestAlertedPercentResetsWhenSpendFallsBack(t *testing.T) {
	t.Parallel()
	budget := domain.BudgetRecord{AmountLimitMinor: 100_00, ThresholdPercent: 80}
	usage := domain.BudgetUsage{AlertedPercent: 100}
	// Deleting an expense can push spend back under the threshold; the record
	// must clear so a later re-crossing alerts again.
	if got := domain.AlertedPercentFor(budget, usage, 10_00); got != 0 {
		t.Fatalf("AlertedPercentFor below threshold = %d, want 0", got)
	}
}

func TestBudgetThresholdCrossingIgnoresZeroLimit(t *testing.T) {
	t.Parallel()
	// Guards the division; validateBudget prevents this on the write path but
	// the projection must not panic on a malformed historical event.
	if _, crossed := domain.BudgetThresholdCrossing(domain.BudgetRecord{}, domain.BudgetUsage{}, 100); crossed {
		t.Fatal("a zero limit must not alert")
	}
}
