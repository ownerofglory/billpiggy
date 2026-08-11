package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsService exposes analytics read models without coupling them to expense writes.
type AnalyticsService struct {
	repository outbound.AnalyticsRepository
	budgets    outbound.BudgetRepository
	expenses   outbound.ExpenseRepository
	now        func() time.Time
}

// NewAnalyticsService creates an analytics query service.
func NewAnalyticsService(repository outbound.AnalyticsRepository, budgets outbound.BudgetRepository, expenses outbound.ExpenseRepository) (*AnalyticsService, error) {
	if repository == nil || budgets == nil || expenses == nil {
		return nil, errors.New("analytics, budget, and expense repositories are required")
	}
	return &AnalyticsService{repository: repository, budgets: budgets, expenses: expenses, now: time.Now}, nil
}

// Bounds applied to caller-supplied "top N" limits so a request cannot force
// an unbounded scan or an absurdly large response.
const (
	defaultTopExpensesLimit     = 10
	maxTopExpensesLimit         = 50
	defaultCategoryChangesLimit = 10
	maxCategoryChangesLimit     = 50
)

// ListBudgetSuggestions returns threshold-driven spending suggestions for the current month.
func (s *AnalyticsService) ListBudgetSuggestions(ctx context.Context, ownerID string) ([]domain.BudgetSuggestion, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	now := s.now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: domain.AnalyticsMonth, From: start, To: now})
	if err != nil {
		return nil, err
	}
	spent := map[string]int64{}
	for _, rollup := range rollups {
		spent[rollup.CategoryID] += rollup.AmountMinor
	}
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return nil, err
	}
	values := []domain.BudgetSuggestion{}
	for _, budget := range budgets {
		value := spent[budget.CategoryID]
		percent := int(value * 100 / budget.AmountLimitMinor)
		if percent < budget.ThresholdPercent {
			continue
		}
		message := "You are approaching this budget."
		if percent >= 100 {
			message = "You have exceeded this budget."
		}
		values = append(values, domain.BudgetSuggestion{BudgetID: budget.ID, CategoryID: budget.CategoryID, Currency: budget.Currency, SpentMinor: value, LimitMinor: budget.AmountLimitMinor, PercentUsed: percent, Message: message})
	}
	return values, nil
}

// ListExpenseRollups returns category/tag rollups for the requested owner and time window.
func (s *AnalyticsService) ListExpenseRollups(ctx context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error) {
	if filter.OwnerID == "" {
		return nil, ErrForbidden
	}
	if !validPeriod(filter.Period) {
		return nil, errors.New("invalid analytics period")
	}
	return s.repository.ListExpenseRollups(ctx, filter)
}

// ComparePeriods sums spend in the current period bucket against the
// immediately preceding bucket of the same granularity, one total per
// currency observed in either bucket.
func (s *AnalyticsService) ComparePeriods(ctx context.Context, ownerID string, period domain.AnalyticsPeriod) (domain.PeriodComparison, error) {
	if ownerID == "" {
		return domain.PeriodComparison{}, ErrForbidden
	}
	if !validPeriod(period) {
		return domain.PeriodComparison{}, errors.New("invalid analytics period")
	}
	currentStart := domain.RollupPeriodStart(period, s.now().UTC())
	previousStart := previousPeriodStart(period, currentStart)
	current, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: period, From: currentStart, To: currentStart})
	if err != nil {
		return domain.PeriodComparison{}, err
	}
	previous, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: period, From: previousStart, To: previousStart})
	if err != nil {
		return domain.PeriodComparison{}, err
	}
	currentByCurrency := sumByCurrency(current)
	previousByCurrency := sumByCurrency(previous)
	totals := make([]domain.PeriodComparisonTotal, 0, len(currentByCurrency))
	for _, currency := range unionCurrencyKeys(currentByCurrency, previousByCurrency) {
		totals = append(totals, domain.PeriodComparisonTotal{
			Currency:      currency,
			CurrentMinor:  currentByCurrency[currency],
			PreviousMinor: previousByCurrency[currency],
			PercentChange: percentChange(currentByCurrency[currency], previousByCurrency[currency]),
		})
	}
	return domain.PeriodComparison{Period: period, CurrentStart: currentStart, PreviousStart: previousStart, Totals: totals}, nil
}

// TopCategoryChanges ranks categories by current-period spend, alongside
// their spend in the immediately preceding period, for spotting what moved.
func (s *AnalyticsService) TopCategoryChanges(ctx context.Context, ownerID string, period domain.AnalyticsPeriod, limit int) ([]domain.CategoryChange, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	if !validPeriod(period) {
		return nil, errors.New("invalid analytics period")
	}
	limit = clampLimit(limit, defaultCategoryChangesLimit, maxCategoryChangesLimit)
	currentStart := domain.RollupPeriodStart(period, s.now().UTC())
	previousStart := previousPeriodStart(period, currentStart)
	current, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: period, From: currentStart, To: currentStart})
	if err != nil {
		return nil, err
	}
	previous, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: period, From: previousStart, To: previousStart})
	if err != nil {
		return nil, err
	}
	type categoryKey struct{ categoryID, currency string }
	type categoryTotals struct {
		current, previous, count int64
	}
	byCategory := map[categoryKey]*categoryTotals{}
	for _, rollup := range current {
		key := categoryKey{rollup.CategoryID, rollup.Currency}
		entry := byCategory[key]
		if entry == nil {
			entry = &categoryTotals{}
			byCategory[key] = entry
		}
		entry.current += rollup.AmountMinor
		entry.count += rollup.ExpenseCount
	}
	for _, rollup := range previous {
		key := categoryKey{rollup.CategoryID, rollup.Currency}
		entry := byCategory[key]
		if entry == nil {
			entry = &categoryTotals{}
			byCategory[key] = entry
		}
		entry.previous += rollup.AmountMinor
	}
	changes := make([]domain.CategoryChange, 0, len(byCategory))
	for key, entry := range byCategory {
		changes = append(changes, domain.CategoryChange{
			CategoryID:    key.categoryID,
			Currency:      key.currency,
			CurrentMinor:  entry.current,
			PreviousMinor: entry.previous,
			PercentChange: percentChange(entry.current, entry.previous),
			ExpenseCount:  entry.count,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].CurrentMinor != changes[j].CurrentMinor {
			return changes[i].CurrentMinor > changes[j].CurrentMinor
		}
		return changes[i].CategoryID < changes[j].CategoryID
	})
	if len(changes) > limit {
		changes = changes[:limit]
	}
	return changes, nil
}

// BurnRate reports spend so far in the current period bucket, projected
// forward at the observed daily average, against any budgets configured for
// the same granularity. One entry per currency observed across spend and
// matching budgets.
func (s *AnalyticsService) BurnRate(ctx context.Context, ownerID string, period domain.AnalyticsPeriod) ([]domain.BurnRate, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	if !validPeriod(period) {
		return nil, errors.New("invalid analytics period")
	}
	now := s.now().UTC()
	start := domain.RollupPeriodStart(period, now)
	end := periodEnd(period, start)
	daysTotal := int(end.Sub(start).Hours() / 24)
	if daysTotal < 1 {
		daysTotal = 1
	}
	daysElapsed := int(now.Sub(start).Hours()/24) + 1
	if daysElapsed > daysTotal {
		daysElapsed = daysTotal
	}
	if daysElapsed < 1 {
		daysElapsed = 1
	}
	rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: period, From: start, To: start})
	if err != nil {
		return nil, err
	}
	spent := sumByCurrency(rollups)
	expected := map[string]int64{}
	if budgetPeriod := budgetPeriodFor(period); budgetPeriod != "" {
		budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
		if err != nil {
			return nil, err
		}
		for _, budget := range budgets {
			if budget.Period == budgetPeriod {
				expected[budget.Currency] += budget.AmountLimitMinor
			}
		}
	}
	rates := make([]domain.BurnRate, 0, len(spent))
	for _, currency := range unionCurrencyKeys(spent, expected) {
		spentMinor := spent[currency]
		average := spentMinor / int64(daysElapsed)
		rates = append(rates, domain.BurnRate{
			Period: period, PeriodStart: start, PeriodEnd: end, Currency: currency,
			SpentMinor: spentMinor, DaysElapsed: daysElapsed, DaysTotal: daysTotal,
			AveragePerDayMinor: average, ProjectedTotalMinor: average * int64(daysTotal),
			ExpectedMinor: expected[currency],
		})
	}
	return rates, nil
}

// DailyTotals sums spend across all categories onto each calendar day in
// [from, to], for a calendar-heatmap style view.
func (s *AnalyticsService) DailyTotals(ctx context.Context, ownerID string, from, to time.Time) ([]domain.DailyTotal, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: domain.AnalyticsDay, From: from, To: to})
	if err != nil {
		return nil, err
	}
	type dayKey struct {
		dayNano  int64
		currency string
	}
	byDay := map[dayKey]*domain.DailyTotal{}
	for _, rollup := range rollups {
		key := dayKey{rollup.PeriodStart.UnixNano(), rollup.Currency}
		entry := byDay[key]
		if entry == nil {
			entry = &domain.DailyTotal{Date: rollup.PeriodStart, Currency: rollup.Currency}
			byDay[key] = entry
		}
		entry.AmountMinor += rollup.AmountMinor
		entry.ExpenseCount += rollup.ExpenseCount
	}
	values := make([]domain.DailyTotal, 0, len(byDay))
	for _, entry := range byDay {
		values = append(values, *entry)
	}
	sort.Slice(values, func(i, j int) bool {
		if !values[i].Date.Equal(values[j].Date) {
			return values[i].Date.Before(values[j].Date)
		}
		return values[i].Currency < values[j].Currency
	})
	return values, nil
}

// WeekdayBreakdown sums spend across all categories onto each weekday within
// [from, to]. Every weekday is present for every currency observed in the
// range, zero-filled where there was no spend, so callers always get a
// complete Sunday-through-Saturday series.
func (s *AnalyticsService) WeekdayBreakdown(ctx context.Context, ownerID string, from, to time.Time) ([]domain.WeekdayTotal, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: domain.AnalyticsDay, From: from, To: to})
	if err != nil {
		return nil, err
	}
	type weekdayKey struct {
		weekday  time.Weekday
		currency string
	}
	sums := map[weekdayKey]*domain.WeekdayTotal{}
	currencies := map[string]bool{}
	for _, rollup := range rollups {
		currencies[rollup.Currency] = true
		key := weekdayKey{rollup.PeriodStart.Weekday(), rollup.Currency}
		entry := sums[key]
		if entry == nil {
			entry = &domain.WeekdayTotal{Weekday: int(key.weekday), Currency: key.currency}
			sums[key] = entry
		}
		entry.AmountMinor += rollup.AmountMinor
		entry.ExpenseCount += rollup.ExpenseCount
	}
	currencyList := make([]string, 0, len(currencies))
	for currency := range currencies {
		currencyList = append(currencyList, currency)
	}
	sort.Strings(currencyList)
	values := make([]domain.WeekdayTotal, 0, len(currencyList)*7)
	for _, currency := range currencyList {
		for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
			key := weekdayKey{weekday, currency}
			if entry, ok := sums[key]; ok {
				values = append(values, *entry)
			} else {
				values = append(values, domain.WeekdayTotal{Weekday: int(weekday), Currency: currency})
			}
		}
	}
	return values, nil
}

// TopExpenses returns the largest individual expenses in [from, to].
func (s *AnalyticsService) TopExpenses(ctx context.Context, ownerID string, from, to time.Time, limit int) ([]domain.ExpenseRecord, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	limit = clampLimit(limit, defaultTopExpensesLimit, maxTopExpensesLimit)
	return s.expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: ownerID, From: from, To: to, SortBy: outbound.ExpenseSortAmount, Limit: limit})
}

// BudgetProgress reports every one of the owner's budgets against its spend
// for its own current period window, regardless of how close it is to its
// alert threshold. Unlike ListBudgetSuggestions, which only surfaces budgets
// at or over their threshold within a fixed calendar-month window, this uses
// each budget's own BudgetWindow so a weekly or custom budget is compared
// against its own window rather than the current month.
func (s *AnalyticsService) BudgetProgress(ctx context.Context, ownerID string) ([]domain.BudgetProgress, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	values := make([]domain.BudgetProgress, 0, len(budgets))
	for _, budget := range budgets {
		start, end := domain.BudgetWindow(budget, now)
		inclusiveTo := domain.RollupPeriodStart(domain.AnalyticsDay, end.Add(-time.Nanosecond))
		rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: domain.AnalyticsDay, CategoryID: budget.CategoryID, From: start, To: inclusiveTo})
		if err != nil {
			return nil, err
		}
		var spent int64
		for _, rollup := range rollups {
			if rollup.Currency == budget.Currency {
				spent += rollup.AmountMinor
			}
		}
		percent := 0
		if budget.AmountLimitMinor > 0 {
			percent = int(spent * 100 / budget.AmountLimitMinor)
		}
		values = append(values, domain.BudgetProgress{
			BudgetID: budget.ID, CategoryID: budget.CategoryID, Name: budget.Name, Currency: budget.Currency,
			Period: budget.Period, PeriodStart: start, PeriodEnd: end,
			SpentMinor: spent, LimitMinor: budget.AmountLimitMinor, PercentUsed: percent,
		})
	}
	return values, nil
}

// validPeriod reports whether period is one of the four supported rollup
// granularities.
func validPeriod(period domain.AnalyticsPeriod) bool {
	switch period {
	case domain.AnalyticsDay, domain.AnalyticsWeek, domain.AnalyticsMonth, domain.AnalyticsYear:
		return true
	default:
		return false
	}
}

// previousPeriodStart returns the start of the bucket immediately preceding
// start, at the same granularity.
func previousPeriodStart(period domain.AnalyticsPeriod, start time.Time) time.Time {
	switch period {
	case domain.AnalyticsDay:
		return start.AddDate(0, 0, -1)
	case domain.AnalyticsWeek:
		return start.AddDate(0, 0, -7)
	case domain.AnalyticsMonth:
		return start.AddDate(0, -1, 0)
	case domain.AnalyticsYear:
		return start.AddDate(-1, 0, 0)
	default:
		return start
	}
}

// periodEnd returns the exclusive end of the bucket of period that starts at
// start. domain.ReportPeriodEnd covers week/month/year; day is handled here
// since periodic reports are never generated at day granularity.
func periodEnd(period domain.AnalyticsPeriod, start time.Time) time.Time {
	if period == domain.AnalyticsDay {
		return start.AddDate(0, 0, 1)
	}
	return domain.ReportPeriodEnd(period, start)
}

// budgetPeriodFor maps an analytics granularity to the budget period that
// covers the same window, returning "" for granularities no budget period
// matches.
func budgetPeriodFor(period domain.AnalyticsPeriod) domain.BudgetPeriod {
	switch period {
	case domain.AnalyticsDay:
		return domain.BudgetDaily
	case domain.AnalyticsWeek:
		return domain.BudgetWeekly
	case domain.AnalyticsMonth:
		return domain.BudgetMonthly
	case domain.AnalyticsYear:
		return domain.BudgetYearly
	default:
		return ""
	}
}

// sumByCurrency totals rollup amounts per currency, collapsing every other
// dimension (category or tag).
func sumByCurrency(rollups []domain.ExpenseRollup) map[string]int64 {
	sums := map[string]int64{}
	for _, rollup := range rollups {
		sums[rollup.Currency] += rollup.AmountMinor
	}
	return sums
}

// unionCurrencyKeys returns the sorted union of keys across maps, so
// multi-currency responses are built in a stable order.
func unionCurrencyKeys(maps ...map[string]int64) []string {
	seen := map[string]bool{}
	keys := make([]string, 0)
	for _, m := range maps {
		for currency := range m {
			if !seen[currency] {
				seen[currency] = true
				keys = append(keys, currency)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// percentChange reports the percentage change from previous to current, or
// nil when previous is zero and there is nothing to compare against.
func percentChange(current, previous int64) *float64 {
	if previous == 0 {
		return nil
	}
	value := float64(current-previous) / float64(previous) * 100
	return &value
}

// clampLimit returns def when limit is not positive, max when limit exceeds
// it, and limit otherwise.
func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}
