package inbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsService is everything an HTTP handler needs from analytics queries.
type AnalyticsService interface {
	ListBudgetSuggestions(ctx context.Context, ownerID string) ([]domain.BudgetSuggestion, error)
	ListExpenseRollups(ctx context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error)
	ComparePeriods(ctx context.Context, ownerID string, period domain.AnalyticsPeriod) (domain.PeriodComparison, error)
	TopCategoryChanges(ctx context.Context, ownerID string, period domain.AnalyticsPeriod, limit int) ([]domain.CategoryChange, error)
	BurnRate(ctx context.Context, ownerID string, period domain.AnalyticsPeriod) ([]domain.BurnRate, error)
	DailyTotals(ctx context.Context, ownerID string, from, to time.Time) ([]domain.DailyTotal, error)
	WeekdayBreakdown(ctx context.Context, ownerID string, from, to time.Time) ([]domain.WeekdayTotal, error)
	TopExpenses(ctx context.Context, ownerID string, from, to time.Time, limit int) ([]domain.ExpenseRecord, error)
	BudgetProgress(ctx context.Context, ownerID string) ([]domain.BudgetProgress, error)
}
