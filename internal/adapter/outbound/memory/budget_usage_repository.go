package memory

import (
	"context"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// usageKey identifies one budget period.
type usageKey struct {
	budgetID    string
	periodStart int64
}

// BudgetUsageRepository is an in-memory budget-usage projection.
//
// It reads budgets from a BudgetRepository so tests can wire the two together
// exactly as the PostgreSQL adapters share one database.
type BudgetUsageRepository struct {
	mu            sync.RWMutex
	budgets       *BudgetRepository
	contributions map[string]domain.ExpenseContribution
	usage         map[usageKey]domain.BudgetUsage
}

// NewBudgetUsageRepository creates a budget-usage projection over a budget store.
func NewBudgetUsageRepository(budgets *BudgetRepository) *BudgetUsageRepository {
	return &BudgetUsageRepository{
		budgets:       budgets,
		contributions: map[string]domain.ExpenseContribution{},
		usage:         map[usageKey]domain.BudgetUsage{},
	}
}

// LoadContribution returns the expense contribution the budgets context has recorded.
func (r *BudgetUsageRepository) LoadContribution(_ context.Context, expenseID string) (domain.ExpenseContribution, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.contributions[expenseID]
	return value, ok, nil
}

// SaveContribution stores or deactivates an expense contribution.
func (r *BudgetUsageRepository) SaveContribution(_ context.Context, contribution domain.ExpenseContribution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contributions[contribution.ExpenseID] = contribution
	return nil
}

// ListBudgetsForCategory returns live budgets an owner holds for one category.
func (r *BudgetUsageRepository) ListBudgetsForCategory(ctx context.Context, ownerID, categoryID string) ([]domain.BudgetRecord, error) {
	if ownerID == "" || categoryID == "" || r.budgets == nil {
		return nil, nil
	}
	all, err := r.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return nil, err
	}
	matching := make([]domain.BudgetRecord, 0, len(all))
	for _, budget := range all {
		if budget.CategoryID == categoryID && budget.OwnerID == ownerID {
			matching = append(matching, budget)
		}
	}
	return matching, nil
}

// GetBudget returns one live budget without owner scoping.
func (r *BudgetUsageRepository) GetBudget(_ context.Context, budgetID string) (domain.BudgetRecord, bool, error) {
	if r.budgets == nil {
		return domain.BudgetRecord{}, false, nil
	}
	r.budgets.mu.RLock()
	defer r.budgets.mu.RUnlock()
	budget, ok := r.budgets.budgets[budgetID]
	if !ok || budget.DeletedAt != nil {
		return domain.BudgetRecord{}, false, nil
	}
	return budget, true, nil
}

// SumContributions totals active contributions in the half-open window [from, to).
func (r *BudgetUsageRepository) SumContributions(_ context.Context, ownerID, categoryID string, from, to time.Time) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total int64
	for _, contribution := range r.contributions {
		if !contribution.Active || contribution.OwnerID != ownerID || contribution.CategoryID != categoryID {
			continue
		}
		if contribution.OccurredAt.Before(from) || !contribution.OccurredAt.Before(to) {
			continue
		}
		total += contribution.AmountMinor
	}
	return total, nil
}

// LoadUsage returns the stored usage row for a budget period.
func (r *BudgetUsageRepository) LoadUsage(_ context.Context, budgetID string, periodStart time.Time) (domain.BudgetUsage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.usage[usageKey{budgetID: budgetID, periodStart: periodStart.UnixNano()}]
	if !ok {
		return domain.BudgetUsage{BudgetID: budgetID, PeriodStart: periodStart}, false, nil
	}
	return value, true, nil
}

// SaveUsage writes the recomputed spend and alert level for a period.
func (r *BudgetUsageRepository) SaveUsage(_ context.Context, usage domain.BudgetUsage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage[usageKey{budgetID: usage.BudgetID, periodStart: usage.PeriodStart.UnixNano()}] = usage
	return nil
}

// DeleteUsage removes every usage row for a budget.
func (r *BudgetUsageRepository) DeleteUsage(_ context.Context, budgetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.usage {
		if key.budgetID == budgetID {
			delete(r.usage, key)
		}
	}
	return nil
}

// Usage returns every stored usage row, for test assertions.
func (r *BudgetUsageRepository) Usage() []domain.BudgetUsage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.BudgetUsage, 0, len(r.usage))
	for _, value := range r.usage {
		values = append(values, value)
	}
	return values
}

// Snapshot copies the projection and returns a function restoring it.
func (r *BudgetUsageRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	contributions := make(map[string]domain.ExpenseContribution, len(r.contributions))
	for id, value := range r.contributions {
		contributions[id] = value
	}
	usage := make(map[usageKey]domain.BudgetUsage, len(r.usage))
	for key, value := range r.usage {
		usage[key] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.contributions, r.usage = contributions, usage
	}
}
