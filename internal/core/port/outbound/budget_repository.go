package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// BudgetRepository owns the budget projection.
type BudgetRepository interface {
	CreateBudget(context.Context, domain.BudgetRecord) error
	ListBudgets(context.Context, string, []string) ([]domain.BudgetRecord, error)
	GetBudget(context.Context, string, string, []string) (domain.BudgetRecord, error)
	UpdateBudget(context.Context, domain.BudgetRecord) error
	DeleteBudget(context.Context, string, string) error
}
