package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// BudgetService is everything an HTTP handler needs from budget commands and queries.
type BudgetService interface {
	CreateBudget(ctx context.Context, owner domain.AppUser, budget domain.BudgetRecord) (domain.BudgetRecord, error)
	ListBudgets(ctx context.Context, viewer domain.AppUser) ([]domain.BudgetRecord, error)
	GetBudget(ctx context.Context, viewer domain.AppUser, budgetID string) (domain.BudgetRecord, error)
	UpdateBudget(ctx context.Context, owner domain.AppUser, budgetID string, update domain.BudgetRecord) (domain.BudgetRecord, error)
	DeleteBudget(ctx context.Context, owner domain.AppUser, budgetID string) error
}
