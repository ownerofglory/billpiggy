package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// ExpenseRepository is a concurrency-safe in-memory expense projection.
type ExpenseRepository struct {
	mu       sync.RWMutex
	expenses map[string]domain.ExpenseRecord
}

// NewExpenseRepository creates an empty expense projection.
func NewExpenseRepository() *ExpenseRepository {
	return &ExpenseRepository{expenses: make(map[string]domain.ExpenseRecord)}
}

func (r *ExpenseRepository) CreateExpense(_ context.Context, expense domain.ExpenseRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.expenses[expense.ID]; exists {
		return errNotFound
	}
	r.expenses[expense.ID] = cloneExpense(expense)
	return nil
}

func (r *ExpenseRepository) UpdateExpense(_ context.Context, expense domain.ExpenseRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.expenses[expense.ID]; !exists {
		return errNotFound
	}
	r.expenses[expense.ID] = cloneExpense(expense)
	return nil
}

func (r *ExpenseRepository) GetExpense(_ context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	expense, ok := r.expenses[expenseID]
	if !ok || expense.OwnerID != ownerID || expense.DeletedAt != nil {
		return domain.ExpenseRecord{}, errNotFound
	}
	return cloneExpense(expense), nil
}

func (r *ExpenseRepository) ListExpenses(_ context.Context, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	expenses := make([]domain.ExpenseRecord, 0)
	for _, expense := range r.expenses {
		if expense.OwnerID != filter.OwnerID || expense.DeletedAt != nil || !matchesExpense(expense, filter) {
			continue
		}
		expenses = append(expenses, cloneExpense(expense))
	}
	sort.Slice(expenses, func(i, j int) bool { return expenses[i].OccurredAt.After(expenses[j].OccurredAt) })
	start := min(filter.Offset, len(expenses))
	end := min(start+filter.Limit, len(expenses))
	return expenses[start:end], nil
}

func (r *ExpenseRepository) DeleteExpense(_ context.Context, ownerID, expenseID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	expense, ok := r.expenses[expenseID]
	if !ok || expense.OwnerID != ownerID || expense.DeletedAt != nil {
		return errNotFound
	}
	now := expense.UpdatedAt
	expense.DeletedAt = &now
	r.expenses[expenseID] = expense
	return nil
}

// Snapshot copies the projection and returns a function restoring it.
func (r *ExpenseRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	saved := make(map[string]domain.ExpenseRecord, len(r.expenses))
	for id, expense := range r.expenses {
		saved[id] = cloneExpense(expense)
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.expenses = saved
	}
}

func matchesExpense(expense domain.ExpenseRecord, filter outbound.ExpenseListFilter) bool {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query != "" && !strings.Contains(strings.ToLower(expense.Title), query) && !strings.Contains(strings.ToLower(expense.CategoryName), query) {
		return false
	}
	if filter.CategoryID != "" && expense.CategoryID != filter.CategoryID {
		return false
	}
	if !filter.From.IsZero() && expense.OccurredAt.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && !expense.OccurredAt.Before(filter.To) {
		return false
	}
	for _, wantedTagID := range filter.TagIDs {
		found := false
		for _, tagID := range expense.TagIDs {
			if tagID == wantedTagID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func cloneExpense(expense domain.ExpenseRecord) domain.ExpenseRecord {
	expense.TagIDs = append([]string(nil), expense.TagIDs...)
	expense.Items = append([]domain.ExpenseItem(nil), expense.Items...)
	return expense
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
