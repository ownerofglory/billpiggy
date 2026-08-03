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
	OwnerID string
	// PeriodStart is the inclusive start of the aggregation window.
	PeriodStart time.Time
	// Period identifies the rollup window size.
	Period AnalyticsPeriod
	// CategoryID optionally identifies the aggregated category.
	CategoryID string
	// TagID optionally identifies the aggregated tag.
	TagID string
	// Currency is the ISO 4217 currency code.
	Currency string
	// AmountMinor is the aggregate in currency minor units.
	AmountMinor int64
	// ExpenseCount is the number of included expenses.
	ExpenseCount int64
}

// BudgetSuggestion identifies a budget that is approaching or over its threshold.
type BudgetSuggestion struct {
	BudgetID, CategoryID, Currency, Message string
	SpentMinor, LimitMinor                  int64
	PercentUsed                             int
}
