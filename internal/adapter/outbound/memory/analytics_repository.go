package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsRepository is an in-memory analytics projection for local development and tests.
type AnalyticsRepository struct {
	mu      sync.RWMutex
	rollups []domain.ExpenseRollup
}

// NewAnalyticsRepository creates an empty analytics projection.
func NewAnalyticsRepository() *AnalyticsRepository { return &AnalyticsRepository{} }

// ListExpenseRollups returns rollups matching the requested owner and filters.
func (r *AnalyticsRepository) ListExpenseRollups(_ context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.ExpenseRollup, 0)
	for _, value := range r.rollups {
		if value.OwnerID != filter.OwnerID || value.Period != filter.Period || value.PeriodStart.Before(filter.From) || value.PeriodStart.After(filter.To) {
			continue
		}
		if filter.CategoryID != "" && value.CategoryID != filter.CategoryID {
			continue
		}
		if len(filter.TagIDs) > 0 && !containsGroup(filter.TagIDs, value.TagID) {
			continue
		}
		values = append(values, value)
	}
	return values, nil
}
