package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// BudgetUsageRepository owns the budgets context's own spend projection.
//
// The budgets context keeps a private mirror of the expense contributions it
// has seen rather than reading the analytics schema: bounded contexts do not
// query one another's write models, and a small duplicated table is the correct
// price for that isolation.
type BudgetUsageRepository interface {
	// LoadContribution returns the expense contribution the budgets context has
	// recorded, reporting false when it has never seen the expense.
	LoadContribution(ctx context.Context, expenseID string) (domain.ExpenseContribution, bool, error)
	// SaveContribution stores or deactivates an expense contribution.
	SaveContribution(ctx context.Context, contribution domain.ExpenseContribution) error
	// ListBudgetsForCategory returns live budgets an owner holds for a category.
	ListBudgetsForCategory(ctx context.Context, ownerID, categoryID string) ([]domain.BudgetRecord, error)
	// GetBudget returns one live budget regardless of owner scoping, for use by
	// projections reacting to budget events.
	GetBudget(ctx context.Context, budgetID string) (domain.BudgetRecord, bool, error)
	// SumContributions totals active contributions in the half-open window
	// [from, to) for one owner and category.
	SumContributions(ctx context.Context, ownerID, categoryID string, from, to time.Time) (int64, error)
	// LoadUsage returns the stored usage row for a budget period, reporting
	// false when the period has no row yet.
	LoadUsage(ctx context.Context, budgetID string, periodStart time.Time) (domain.BudgetUsage, bool, error)
	// SaveUsage writes the recomputed spend and alert level for a period.
	SaveUsage(ctx context.Context, usage domain.BudgetUsage) error
	// DeleteUsage removes every usage row for a budget.
	DeleteUsage(ctx context.Context, budgetID string) error
}
