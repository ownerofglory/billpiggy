package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AnalyticsRepository owns analytics projections and never writes expense aggregates.
type AnalyticsRepository interface {
	ListExpenseRollups(context.Context, AnalyticsFilter) ([]domain.ExpenseRollup, error)
}

// AnalyticsFilter keeps analytics queries scoped to one owner and time range.
type AnalyticsFilter struct {
	OwnerID    string
	Period     domain.AnalyticsPeriod
	From       time.Time
	To         time.Time
	CategoryID string
	TagIDs     []string
}
