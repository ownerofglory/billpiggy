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
func (r *BudgetRepository) ListBudgets(_ context.Context, ownerID string) ([]domain.BudgetRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := []domain.BudgetRecord{}
	for _, value := range r.budgets {
		if value.OwnerID == ownerID && value.DeletedAt == nil {
			values = append(values, value)
		}
	}
	return values, nil
}
func (r *BudgetRepository) GetBudget(_ context.Context, ownerID, id string) (domain.BudgetRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.budgets[id]
	if !ok || value.OwnerID != ownerID || value.DeletedAt != nil {
		return domain.BudgetRecord{}, errNotFound
	}
	return value, nil
}
func (r *BudgetRepository) UpdateBudget(_ context.Context, value domain.BudgetRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budgets[value.ID] = value
	return nil
}
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
