package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// ExpenseService is everything an HTTP handler needs from expense commands
// and queries. WithObjectReferences is wiring-only and excluded: only
// cmd/billpiggy calls it, at startup.
type ExpenseService interface {
	CreateExpense(ctx context.Context, ownerID string, command service.CreateExpenseCommand) (domain.ExpenseRecord, error)
	UpdateExpense(ctx context.Context, ownerID, expenseID string, command service.UpdateExpenseCommand) (domain.ExpenseRecord, error)
	DeleteExpense(ctx context.Context, ownerID, expenseID string) error
	ListExpenses(ctx context.Context, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error)
	GetExpense(ctx context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error)
	ListExpensesForViewer(ctx context.Context, viewer domain.AppUser, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error)
	GetExpenseForViewer(ctx context.Context, viewer domain.AppUser, expenseID string) (domain.ExpenseRecord, error)
	AttachReceipt(ctx context.Context, ownerID, expenseID, objectKey string) (domain.ExpenseRecord, error)
}
