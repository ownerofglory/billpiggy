package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// rollupKey identifies one analytics bucket.
type rollupKey struct {
	ownerID     string
	periodStart int64
	period      domain.AnalyticsPeriod
	dimensionID string
	currency    string
	tag         bool
}

// AnalyticsRepository is an in-memory analytics projection for local development and tests.
//
// It implements the same write port the PostgreSQL adapter does, so the
// analytics projection is exercised end to end without a database.
type AnalyticsRepository struct {
	mu            sync.RWMutex
	rollups       map[rollupKey]domain.ExpenseRollup
	contributions map[string]domain.ExpenseContribution
}

// NewAnalyticsRepository creates an empty analytics projection.
func NewAnalyticsRepository() *AnalyticsRepository {
	return &AnalyticsRepository{
		rollups:       map[rollupKey]domain.ExpenseRollup{},
		contributions: map[string]domain.ExpenseContribution{},
	}
}

// ListExpenseRollups returns rollups matching the requested owner and filters.
func (r *AnalyticsRepository) ListExpenseRollups(_ context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	wantTags := len(filter.TagIDs) > 0
	values := make([]domain.ExpenseRollup, 0)
	for key, value := range r.rollups {
		if key.tag != wantTags {
			continue
		}
		if value.OwnerID != filter.OwnerID || value.Period != filter.Period {
			continue
		}
		if value.PeriodStart.Before(filter.From) || value.PeriodStart.After(filter.To) {
			continue
		}
		if !wantTags && filter.CategoryID != "" && value.CategoryID != filter.CategoryID {
			continue
		}
		if wantTags && !containsGroup(filter.TagIDs, value.TagID) {
			continue
		}
		values = append(values, value)
	}
	return values, nil
}

// LoadContribution returns the contribution currently reflected in the rollups.
func (r *AnalyticsRepository) LoadContribution(_ context.Context, expenseID string) (domain.ExpenseContribution, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.contributions[expenseID]
	if !ok {
		return domain.ExpenseContribution{}, false, nil
	}
	value.TagIDs = append([]string(nil), value.TagIDs...)
	return value, true, nil
}

// SaveContribution records the contribution now reflected in the rollups.
func (r *AnalyticsRepository) SaveContribution(_ context.Context, contribution domain.ExpenseContribution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	contribution.TagIDs = append([]string(nil), contribution.TagIDs...)
	r.contributions[contribution.ExpenseID] = contribution
	return nil
}

// AddRollupDelta applies a signed amount and count to one rollup bucket.
func (r *AnalyticsRepository) AddRollupDelta(_ context.Context, delta domain.RollupDelta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	dimensionID, isTag := delta.CategoryID, false
	if delta.TagID != "" {
		dimensionID, isTag = delta.TagID, true
	}
	key := rollupKey{
		ownerID:     delta.OwnerID,
		periodStart: delta.PeriodStart.UnixNano(),
		period:      delta.Period,
		dimensionID: dimensionID,
		currency:    delta.Currency,
		tag:         isTag,
	}
	value, ok := r.rollups[key]
	if !ok {
		value = domain.ExpenseRollup{
			OwnerID:     delta.OwnerID,
			PeriodStart: delta.PeriodStart,
			Period:      delta.Period,
			Currency:    delta.Currency,
		}
		if isTag {
			value.TagID = dimensionID
		} else {
			value.CategoryID = dimensionID
		}
	}
	value.AmountMinor += delta.AmountMinor
	value.ExpenseCount += delta.ExpenseCount
	r.rollups[key] = value
	return nil
}

// Snapshot copies the projection and returns a function restoring it.
func (r *AnalyticsRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rollups := make(map[rollupKey]domain.ExpenseRollup, len(r.rollups))
	for key, value := range r.rollups {
		rollups[key] = value
	}
	contributions := make(map[string]domain.ExpenseContribution, len(r.contributions))
	for id, value := range r.contributions {
		contributions[id] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.rollups, r.contributions = rollups, contributions
	}
}
