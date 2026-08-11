package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func newTestAnalyticsService(t *testing.T) (*service.AnalyticsService, *memory.AnalyticsRepository, *memory.BudgetRepository, *memory.ExpenseRepository) {
	t.Helper()
	analytics := memory.NewAnalyticsRepository()
	budgets := memory.NewBudgetRepository()
	expenses := memory.NewExpenseRepository()
	svc, err := service.NewAnalyticsService(analytics, budgets, expenses)
	if err != nil {
		t.Fatalf("new analytics service: %v", err)
	}
	return svc, analytics, budgets, expenses
}

func seedRollup(t *testing.T, repo *memory.AnalyticsRepository, ownerID, categoryID string, period domain.AnalyticsPeriod, periodStart time.Time, currency string, amountMinor, count int64) {
	t.Helper()
	if err := repo.AddRollupDelta(context.Background(), domain.RollupDelta{
		OwnerID: ownerID, CategoryID: categoryID, Currency: currency, Period: period,
		PeriodStart: periodStart, AmountMinor: amountMinor, ExpenseCount: count,
	}); err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
}

func TestAnalyticsServiceComparePeriodsComputesPercentChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, _, _ := newTestAnalyticsService(t)
	currentStart := domain.RollupPeriodStart(domain.AnalyticsWeek, time.Now())
	previousStart := currentStart.AddDate(0, 0, -7)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsWeek, currentStart, "EUR", 15000, 3)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsWeek, previousStart, "EUR", 10000, 2)

	result, err := svc.ComparePeriods(ctx, "owner-1", domain.AnalyticsWeek)
	if err != nil {
		t.Fatalf("compare periods: %v", err)
	}
	if !result.CurrentStart.Equal(currentStart) || !result.PreviousStart.Equal(previousStart) {
		t.Fatalf("unexpected bucket starts: current=%v previous=%v", result.CurrentStart, result.PreviousStart)
	}
	if len(result.Totals) != 1 {
		t.Fatalf("got %d totals, want 1: %#v", len(result.Totals), result.Totals)
	}
	total := result.Totals[0]
	if total.Currency != "EUR" || total.CurrentMinor != 15000 || total.PreviousMinor != 10000 {
		t.Fatalf("unexpected total: %#v", total)
	}
	if total.PercentChange == nil || *total.PercentChange != 50 {
		t.Fatalf("percent change = %v, want 50", total.PercentChange)
	}
}

func TestAnalyticsServiceComparePeriodsNilChangeWithoutPreviousSpend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, _, _ := newTestAnalyticsService(t)
	currentStart := domain.RollupPeriodStart(domain.AnalyticsMonth, time.Now())
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsMonth, currentStart, "EUR", 5000, 1)

	result, err := svc.ComparePeriods(ctx, "owner-1", domain.AnalyticsMonth)
	if err != nil {
		t.Fatalf("compare periods: %v", err)
	}
	if len(result.Totals) != 1 {
		t.Fatalf("got %d totals, want 1: %#v", len(result.Totals), result.Totals)
	}
	if result.Totals[0].PreviousMinor != 0 || result.Totals[0].PercentChange != nil {
		t.Fatalf("unexpected total with no previous spend: %#v", result.Totals[0])
	}
}

func TestAnalyticsServiceComparePeriodsForbidsEmptyOwner(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newTestAnalyticsService(t)
	if _, err := svc.ComparePeriods(context.Background(), "", domain.AnalyticsWeek); err != service.ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestAnalyticsServiceTopCategoryChangesRanksAndLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, _, _ := newTestAnalyticsService(t)
	currentStart := domain.RollupPeriodStart(domain.AnalyticsWeek, time.Now())
	previousStart := currentStart.AddDate(0, 0, -7)
	seedRollup(t, analytics, "owner-1", "cat-groceries", domain.AnalyticsWeek, currentStart, "EUR", 15600, 4)
	seedRollup(t, analytics, "owner-1", "cat-groceries", domain.AnalyticsWeek, previousStart, "EUR", 13200, 3)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsWeek, currentStart, "EUR", 2800, 2)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsWeek, previousStart, "EUR", 3200, 2)
	seedRollup(t, analytics, "owner-1", "cat-entertainment", domain.AnalyticsWeek, currentStart, "EUR", 2500, 1)

	changes, err := svc.TopCategoryChanges(ctx, "owner-1", domain.AnalyticsWeek, 2)
	if err != nil {
		t.Fatalf("top category changes: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2 (limit applied): %#v", len(changes), changes)
	}
	if changes[0].CategoryID != "cat-groceries" || changes[0].CurrentMinor != 15600 || changes[0].PreviousMinor != 13200 {
		t.Fatalf("unexpected top change: %#v", changes[0])
	}
	if changes[1].CategoryID != "cat-food" {
		t.Fatalf("second-ranked category = %q, want cat-food", changes[1].CategoryID)
	}
	if changes[1].PercentChange == nil || *changes[1].PercentChange >= 0 {
		t.Fatalf("cat-food declined, want a negative percent change, got %v", changes[1].PercentChange)
	}
}

func TestAnalyticsServiceBurnRateIsInternallyConsistentAndSumsMatchingBudgets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, budgets, _ := newTestAnalyticsService(t)
	monthStart := domain.RollupPeriodStart(domain.AnalyticsMonth, time.Now())
	seedRollup(t, analytics, "owner-1", "cat-groceries", domain.AnalyticsMonth, monthStart, "EUR", 62000, 12)
	if err := budgets.CreateBudget(ctx, domain.BudgetRecord{
		ID: "budget-1", OwnerID: "owner-1", CategoryID: "cat-groceries", Name: "Groceries",
		Currency: "EUR", AmountLimitMinor: 148000, Period: domain.BudgetMonthly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	// A budget for a different period must not contribute to this month's expected amount.
	if err := budgets.CreateBudget(ctx, domain.BudgetRecord{
		ID: "budget-2", OwnerID: "owner-1", CategoryID: "cat-groceries", Name: "Groceries weekly cap",
		Currency: "EUR", AmountLimitMinor: 5000, Period: domain.BudgetWeekly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}

	rates, err := svc.BurnRate(ctx, "owner-1", domain.AnalyticsMonth)
	if err != nil {
		t.Fatalf("burn rate: %v", err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d burn rates, want 1: %#v", len(rates), rates)
	}
	rate := rates[0]
	if rate.Currency != "EUR" || rate.SpentMinor != 62000 {
		t.Fatalf("unexpected spend: %#v", rate)
	}
	if rate.ExpectedMinor != 148000 {
		t.Fatalf("expected minor = %d, want 148000 (only the monthly budget)", rate.ExpectedMinor)
	}
	if rate.DaysElapsed < 1 || rate.DaysElapsed > rate.DaysTotal {
		t.Fatalf("days elapsed = %d out of [1, %d]", rate.DaysElapsed, rate.DaysTotal)
	}
	if rate.AveragePerDayMinor != rate.SpentMinor/int64(rate.DaysElapsed) {
		t.Fatalf("average per day = %d, want %d", rate.AveragePerDayMinor, rate.SpentMinor/int64(rate.DaysElapsed))
	}
	if rate.ProjectedTotalMinor != rate.AveragePerDayMinor*int64(rate.DaysTotal) {
		t.Fatalf("projected total = %d, want %d", rate.ProjectedTotalMinor, rate.AveragePerDayMinor*int64(rate.DaysTotal))
	}
}

func TestAnalyticsServiceDailyTotalsSumsAcrossCategories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, _, _ := newTestAnalyticsService(t)
	day1 := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsDay, day1, "EUR", 1200, 1)
	seedRollup(t, analytics, "owner-1", "cat-groceries", domain.AnalyticsDay, day1, "EUR", 3400, 1)
	seedRollup(t, analytics, "owner-1", "cat-food", domain.AnalyticsDay, day2, "EUR", 900, 1)

	totals, err := svc.DailyTotals(ctx, "owner-1", day1, day2)
	if err != nil {
		t.Fatalf("daily totals: %v", err)
	}
	if len(totals) != 2 {
		t.Fatalf("got %d daily totals, want 2: %#v", len(totals), totals)
	}
	if !totals[0].Date.Equal(day1) || totals[0].AmountMinor != 4600 || totals[0].ExpenseCount != 2 {
		t.Fatalf("unexpected day1 total: %#v", totals[0])
	}
	if !totals[1].Date.Equal(day2) || totals[1].AmountMinor != 900 {
		t.Fatalf("unexpected day2 total: %#v", totals[1])
	}
}

func TestAnalyticsServiceWeekdayBreakdownZeroFillsAllSevenDays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, _, _ := newTestAnalyticsService(t)
	// 2026-07-11 is a Saturday.
	saturday := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	seedRollup(t, analytics, "owner-1", "cat-entertainment", domain.AnalyticsDay, saturday, "EUR", 8600, 2)

	totals, err := svc.WeekdayBreakdown(ctx, "owner-1", saturday.AddDate(0, 0, -6), saturday)
	if err != nil {
		t.Fatalf("weekday breakdown: %v", err)
	}
	if len(totals) != 7 {
		t.Fatalf("got %d weekday totals, want 7: %#v", len(totals), totals)
	}
	for _, total := range totals {
		if total.Weekday == int(time.Saturday) {
			if total.AmountMinor != 8600 {
				t.Fatalf("saturday total = %d, want 8600", total.AmountMinor)
			}
			continue
		}
		if total.AmountMinor != 0 {
			t.Fatalf("weekday %v was zero-filled to %d, want 0", total.Weekday, total.AmountMinor)
		}
	}
}

func TestAnalyticsServiceTopExpensesSortsByAmountDescending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _, expenses := newTestAnalyticsService(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	seeded := []struct {
		id     string
		amount int64
	}{{"e1", 3000}, {"e2", 9200}, {"e3", 1500}}
	for i, expense := range seeded {
		if err := expenses.CreateExpense(ctx, domain.ExpenseRecord{
			ID: expense.id, OwnerID: "owner-1", Title: expense.id, AmountMinor: expense.amount,
			Currency: "EUR", OccurredAt: base.AddDate(0, 0, i), Status: domain.ExpenseConfirmed,
		}); err != nil {
			t.Fatalf("create expense: %v", err)
		}
	}

	top, err := svc.TopExpenses(ctx, "owner-1", base, base.AddDate(0, 0, 10), 2)
	if err != nil {
		t.Fatalf("top expenses: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("got %d expenses, want 2 (limit applied): %#v", len(top), top)
	}
	if top[0].ID != "e2" || top[1].ID != "e1" {
		t.Fatalf("unexpected order: %#v", top)
	}
}

func TestAnalyticsServiceBudgetProgressUsesEachBudgetsOwnWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, analytics, budgets, _ := newTestAnalyticsService(t)
	now := time.Now().UTC()

	monthlyStart, _ := domain.BudgetWindow(domain.BudgetRecord{Period: domain.BudgetMonthly}, now)
	weeklyStart, _ := domain.BudgetWindow(domain.BudgetRecord{Period: domain.BudgetWeekly}, now)
	seedRollup(t, analytics, "owner-1", "cat-groceries", domain.AnalyticsDay, monthlyStart, "EUR", 20000, 4)
	seedRollup(t, analytics, "owner-1", "cat-transport", domain.AnalyticsDay, weeklyStart, "EUR", 3000, 1)

	if err := budgets.CreateBudget(ctx, domain.BudgetRecord{
		ID: "budget-monthly", OwnerID: "owner-1", CategoryID: "cat-groceries", Name: "Groceries",
		Currency: "EUR", AmountLimitMinor: 40000, Period: domain.BudgetMonthly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	if err := budgets.CreateBudget(ctx, domain.BudgetRecord{
		ID: "budget-weekly", OwnerID: "owner-1", CategoryID: "cat-transport", Name: "Transport",
		Currency: "EUR", AmountLimitMinor: 2000, Period: domain.BudgetWeekly,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}

	progress, err := svc.BudgetProgress(ctx, "owner-1")
	if err != nil {
		t.Fatalf("budget progress: %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(progress), progress)
	}
	byID := map[string]domain.BudgetProgress{}
	for _, entry := range progress {
		byID[entry.BudgetID] = entry
	}
	monthly := byID["budget-monthly"]
	if monthly.SpentMinor != 20000 || monthly.PercentUsed != 50 {
		t.Fatalf("unexpected monthly progress: %#v", monthly)
	}
	weekly := byID["budget-weekly"]
	// 3000/2000 = 150%, exercising a budget that is already over its limit.
	if weekly.SpentMinor != 3000 || weekly.PercentUsed != 150 {
		t.Fatalf("unexpected weekly progress: %#v", weekly)
	}
}

func TestAnalyticsServiceForbidsEmptyOwnerIDAcrossNewMethods(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newTestAnalyticsService(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := svc.TopCategoryChanges(ctx, "", domain.AnalyticsWeek, 5); err != service.ErrForbidden {
		t.Fatalf("TopCategoryChanges err = %v, want ErrForbidden", err)
	}
	if _, err := svc.BurnRate(ctx, "", domain.AnalyticsMonth); err != service.ErrForbidden {
		t.Fatalf("BurnRate err = %v, want ErrForbidden", err)
	}
	if _, err := svc.DailyTotals(ctx, "", now, now); err != service.ErrForbidden {
		t.Fatalf("DailyTotals err = %v, want ErrForbidden", err)
	}
	if _, err := svc.WeekdayBreakdown(ctx, "", now, now); err != service.ErrForbidden {
		t.Fatalf("WeekdayBreakdown err = %v, want ErrForbidden", err)
	}
	if _, err := svc.TopExpenses(ctx, "", now, now, 5); err != service.ErrForbidden {
		t.Fatalf("TopExpenses err = %v, want ErrForbidden", err)
	}
	if _, err := svc.BudgetProgress(ctx, ""); err != service.ErrForbidden {
		t.Fatalf("BudgetProgress err = %v, want ErrForbidden", err)
	}
}

func TestAnalyticsServiceRejectsInvalidPeriod(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newTestAnalyticsService(t)
	ctx := context.Background()
	if _, err := svc.ComparePeriods(ctx, "owner-1", domain.AnalyticsPeriod("century")); err == nil {
		t.Fatal("ComparePeriods accepted an invalid period")
	}
	if _, err := svc.TopCategoryChanges(ctx, "owner-1", domain.AnalyticsPeriod("century"), 5); err == nil {
		t.Fatal("TopCategoryChanges accepted an invalid period")
	}
	if _, err := svc.BurnRate(ctx, "owner-1", domain.AnalyticsPeriod("century")); err == nil {
		t.Fatal("BurnRate accepted an invalid period")
	}
}
