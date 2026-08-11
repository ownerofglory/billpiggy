package domain

import "time"

// AnalyticsPeriod determines the aggregation window for expense analytics.
type AnalyticsPeriod string

const (
	// AnalyticsDay groups spending by day.
	AnalyticsDay AnalyticsPeriod = "day"
	// AnalyticsWeek groups spending by ISO week.
	AnalyticsWeek AnalyticsPeriod = "week"
	// AnalyticsMonth groups spending by calendar month.
	AnalyticsMonth AnalyticsPeriod = "month"
	// AnalyticsYear groups spending by calendar year.
	AnalyticsYear AnalyticsPeriod = "year"
)

// ExpenseRollup is a precomputed analytics value owned by one user.
type ExpenseRollup struct {
	// OwnerID identifies the user whose expenses were aggregated.
	OwnerID string `json:"ownerID"`
	// PeriodStart is the inclusive start of the aggregation window.
	PeriodStart time.Time `json:"periodStart"`
	// Period identifies the rollup window size.
	Period AnalyticsPeriod `json:"period"`
	// CategoryID optionally identifies the aggregated category.
	CategoryID string `json:"categoryID"`
	// TagID optionally identifies the aggregated tag.
	TagID string `json:"tagID"`
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency"`
	// AmountMinor is the aggregate in currency minor units.
	AmountMinor int64 `json:"amountMinor"`
	// ExpenseCount is the number of included expenses.
	ExpenseCount int64 `json:"expenseCount"`
}

// BudgetSuggestion identifies a budget that is approaching or over its threshold.
type BudgetSuggestion struct {
	BudgetID    string `json:"budgetID"`
	CategoryID  string `json:"categoryID"`
	Currency    string `json:"currency"`
	Message     string `json:"message"`
	SpentMinor  int64  `json:"spentMinor"`
	LimitMinor  int64  `json:"limitMinor"`
	PercentUsed int    `json:"percentUsed"`
}

// PeriodComparison compares the current period bucket against the immediately
// preceding one of the same granularity, one total per currency observed in
// either bucket.
type PeriodComparison struct {
	// Period is the granularity being compared.
	Period AnalyticsPeriod `json:"period"`
	// CurrentStart is the inclusive start of the current bucket.
	CurrentStart time.Time `json:"currentStart"`
	// PreviousStart is the inclusive start of the immediately preceding bucket.
	PreviousStart time.Time `json:"previousStart"`
	// Totals holds one entry per currency observed in either bucket.
	Totals []PeriodComparisonTotal `json:"totals"`
}

// PeriodComparisonTotal is one currency's current-vs-previous spend.
type PeriodComparisonTotal struct {
	// Currency is the ISO 4217 code the amounts are denominated in.
	Currency string `json:"currency"`
	// CurrentMinor is the current bucket's total, in minor currency units.
	CurrentMinor int64 `json:"currentMinor"`
	// PreviousMinor is the previous bucket's total, in minor currency units.
	PreviousMinor int64 `json:"previousMinor"`
	// PercentChange is nil when PreviousMinor is zero, since there is nothing
	// to compare the current amount against.
	PercentChange *float64 `json:"percentChange"`
}

// CategoryChange is one category's current-vs-previous spend, for ranking
// which categories moved the most between two periods.
type CategoryChange struct {
	// CategoryID identifies the compared category.
	CategoryID string `json:"categoryID"`
	// Currency is the ISO 4217 code the amounts are denominated in.
	Currency string `json:"currency"`
	// CurrentMinor is the current bucket's total, in minor currency units.
	CurrentMinor int64 `json:"currentMinor"`
	// PreviousMinor is the previous bucket's total, in minor currency units.
	PreviousMinor int64 `json:"previousMinor"`
	// PercentChange is nil when PreviousMinor is zero.
	PercentChange *float64 `json:"percentChange"`
	// ExpenseCount is the number of expenses in the current bucket.
	ExpenseCount int64 `json:"expenseCount"`
}

// BurnRate summarizes spend-so-far against a period, projected forward at the
// observed daily average, one entry per currency observed.
type BurnRate struct {
	// Period is the granularity the burn rate is computed over.
	Period AnalyticsPeriod `json:"period"`
	// PeriodStart is the inclusive start of the current bucket.
	PeriodStart time.Time `json:"periodStart"`
	// PeriodEnd is the exclusive end of the current bucket.
	PeriodEnd time.Time `json:"periodEnd"`
	// Currency is the ISO 4217 code the amounts are denominated in.
	Currency string `json:"currency"`
	// SpentMinor is spend so far in the current bucket, in minor units.
	SpentMinor int64 `json:"spentMinor"`
	// DaysElapsed is the number of days of the bucket that have occurred so far.
	DaysElapsed int `json:"daysElapsed"`
	// DaysTotal is the bucket's full length in days.
	DaysTotal int `json:"daysTotal"`
	// AveragePerDayMinor is SpentMinor divided by DaysElapsed.
	AveragePerDayMinor int64 `json:"averagePerDayMinor"`
	// ProjectedTotalMinor is AveragePerDayMinor extrapolated across DaysTotal.
	ProjectedTotalMinor int64 `json:"projectedTotalMinor"`
	// ExpectedMinor sums the limits of budgets whose period and currency match
	// this bucket; zero when no such budget is configured.
	ExpectedMinor int64 `json:"expectedMinor"`
}

// DailyTotal is one calendar day's spend across all categories, for a
// calendar-heatmap style view.
type DailyTotal struct {
	// Date is the day the total applies to.
	Date time.Time `json:"date"`
	// Currency is the ISO 4217 code the amount is denominated in.
	Currency string `json:"currency"`
	// AmountMinor is the day's total, in minor currency units.
	AmountMinor int64 `json:"amountMinor"`
	// ExpenseCount is the number of expenses on that day.
	ExpenseCount int64 `json:"expenseCount"`
}

// WeekdayTotal is spend across all categories aggregated onto one weekday
// within a requested date range. Weekday follows Go's time.Weekday encoding:
// 0 is Sunday through 6 is Saturday.
type WeekdayTotal struct {
	// Weekday identifies the day of the week the total applies to, using Go's
	// time.Weekday encoding (0=Sunday..6=Saturday). Plain int rather than
	// time.Weekday itself so the OpenAPI generator can describe the field.
	Weekday int `json:"weekday"`
	// Currency is the ISO 4217 code the amount is denominated in.
	Currency string `json:"currency"`
	// AmountMinor is the weekday's total, in minor currency units.
	AmountMinor int64 `json:"amountMinor"`
	// ExpenseCount is the number of expenses on that weekday.
	ExpenseCount int64 `json:"expenseCount"`
}

// BudgetProgress is one budget's spend against its limit for its current
// period window, regardless of how close it is to the alert threshold.
type BudgetProgress struct {
	// BudgetID identifies the budget.
	BudgetID string `json:"budgetID"`
	// CategoryID is the budget's category.
	CategoryID string `json:"categoryID"`
	// Name is the budget's display name.
	Name string `json:"name"`
	// Currency is the ISO 4217 code the amounts are denominated in.
	Currency string `json:"currency"`
	// Period is the budget's configured window.
	Period BudgetPeriod `json:"period"`
	// PeriodStart is the inclusive start of the budget's current window.
	PeriodStart time.Time `json:"periodStart"`
	// PeriodEnd is the exclusive end of the budget's current window.
	PeriodEnd time.Time `json:"periodEnd"`
	// SpentMinor is spend against the budget's category within the window.
	SpentMinor int64 `json:"spentMinor"`
	// LimitMinor is the budget's configured limit.
	LimitMinor int64 `json:"limitMinor"`
	// PercentUsed is the share of the limit consumed, which can exceed 100.
	PercentUsed int `json:"percentUsed"`
}
