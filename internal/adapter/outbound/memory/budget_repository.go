package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// BudgetRepository is a concurrency-safe in-memory budget projection.
type BudgetRepository struct {
	mu      sync.RWMutex
	budgets map[string]domain.BudgetRecord
}

// NewBudgetRepository creates an empty budget projection.
func NewBudgetRepository() *BudgetRepository {
	return &BudgetRepository{budgets: map[string]domain.BudgetRecord{}}
}
func (r *BudgetRepository) CreateBudget(_ context.Context, value domain.BudgetRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budgets[value.ID] = value
	return nil
}

// ListBudgets returns budgets owned by or shared with the viewer.
func (r *BudgetRepository) ListBudgets(_ context.Context, ownerID string, sharedGroupIDs []string) ([]domain.BudgetRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := []domain.BudgetRecord{}
	for _, value := range r.budgets {
		if (value.OwnerID == ownerID || containsGroup(sharedGroupIDs, value.SharedGroupID)) && value.DeletedAt == nil {
			values = append(values, value)
		}
	}
	return values, nil
}

// GetBudget returns a budget owned by or shared with the viewer.
func (r *BudgetRepository) GetBudget(_ context.Context, ownerID, id string, sharedGroupIDs []string) (domain.BudgetRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.budgets[id]
	if !ok || (value.OwnerID != ownerID && !containsGroup(sharedGroupIDs, value.SharedGroupID)) || value.DeletedAt != nil {
		return domain.BudgetRecord{}, errNotFound
	}
	return value, nil
}

// Snapshot copies the projection and returns a function restoring it.
func (r *BudgetRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	saved := make(map[string]domain.BudgetRecord, len(r.budgets))
	for id, value := range r.budgets {
		saved[id] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.budgets = saved
	}
}

func containsGroup(groupIDs []string, groupID string) bool {
	for _, value := range groupIDs {
		if value == groupID {
			return true
		}
	}
	return false
}

// UpdateBudget replaces a budget projection owned by the budget owner.
func (r *BudgetRepository) UpdateBudget(_ context.Context, value domain.BudgetRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budgets[value.ID] = value
	return nil
}

// DeleteBudget soft-deletes a budget projection owned by the budget owner.
func (r *BudgetRepository) DeleteBudget(_ context.Context, ownerID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.budgets[id]
	if !ok || value.OwnerID != ownerID {
		return errNotFound
	}
	value.DeletedAt = &value.UpdatedAt
	r.budgets[id] = value
	return nil
}
