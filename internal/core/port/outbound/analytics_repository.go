package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AnalyticsRepository owns analytics projections and never writes expense aggregates.
//
// The write methods are used exclusively by the analytics projection, which
// runs inside the outbox transaction; the read method serves the query API.
type AnalyticsRepository interface {
	// ListExpenseRollups returns rollups matching the filter.
	ListExpenseRollups(ctx context.Context, filter AnalyticsFilter) ([]domain.ExpenseRollup, error)
	// LoadContribution returns the contribution currently reflected in the
	// rollups, reporting false when the expense has never been projected.
	LoadContribution(ctx context.Context, expenseID string) (domain.ExpenseContribution, bool, error)
	// SaveContribution records the contribution now reflected in the rollups.
	SaveContribution(ctx context.Context, contribution domain.ExpenseContribution) error
	// AddRollupDelta applies a signed amount and count to one rollup bucket.
	AddRollupDelta(ctx context.Context, delta domain.RollupDelta) error
}

// AnalyticsFilter keeps analytics queries scoped to one owner and time range.
type AnalyticsFilter struct {
	// OwnerID scopes the query to one user.
	OwnerID string
	// Period selects the rollup granularity.
	Period domain.AnalyticsPeriod
	// From and To bound the returned periods inclusively.
	From time.Time
	To   time.Time
	// CategoryID optionally restricts the query to one category.
	CategoryID string
	// TagIDs optionally switches the query to tag rollups for these tags.
	TagIDs []string
}
