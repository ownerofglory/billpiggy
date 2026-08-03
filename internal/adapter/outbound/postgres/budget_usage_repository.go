package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// BudgetUsageRepository persists the budgets context's spend projection.
type BudgetUsageRepository struct{ pool *pgxpool.Pool }

// NewBudgetUsageRepository creates a budget-usage adapter.
func NewBudgetUsageRepository(pool *pgxpool.Pool) *BudgetUsageRepository {
	return &BudgetUsageRepository{pool: pool}
}

// LoadContribution returns the expense contribution the budgets context has recorded.
func (r *BudgetUsageRepository) LoadContribution(ctx context.Context, expenseID string) (domain.ExpenseContribution, bool, error) {
	value := domain.ExpenseContribution{ExpenseID: expenseID}
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select owner_id::text, coalesce(category_id::text, ''), currency, amount_minor, occurred_at, active from budgets.expense_contributions where expense_id = $1`, expenseID).
		Scan(&value.OwnerID, &value.CategoryID, &value.Currency, &value.AmountMinor, &value.OccurredAt, &value.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExpenseContribution{}, false, nil
	}
	if err != nil {
		return domain.ExpenseContribution{}, false, fmt.Errorf("load budget contribution: %w", err)
	}
	return value, true, nil
}

// SaveContribution stores or deactivates an expense contribution.
func (r *BudgetUsageRepository) SaveContribution(ctx context.Context, contribution domain.ExpenseContribution) error {
	occurredAt := contribution.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Unix(0, 0).UTC()
	}
	currency := contribution.Currency
	if currency == "" {
		currency = "XXX"
	}
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into budgets.expense_contributions
			(expense_id, owner_id, category_id, currency, amount_minor, occurred_at, active, updated_at)
		values ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7, now())
		on conflict (expense_id) do update
		   set owner_id = excluded.owner_id, category_id = excluded.category_id, currency = excluded.currency,
		       amount_minor = excluded.amount_minor, occurred_at = excluded.occurred_at,
		       active = excluded.active, updated_at = now()`,
		contribution.ExpenseID, contribution.OwnerID, contribution.CategoryID, currency,
		contribution.AmountMinor, occurredAt, contribution.Active)
	if err != nil {
		return fmt.Errorf("save budget contribution: %w", err)
	}
	return nil
}

// ListBudgetsForCategory returns live budgets an owner holds for one category.
func (r *BudgetUsageRepository) ListBudgetsForCategory(ctx context.Context, ownerID, categoryID string) ([]domain.BudgetRecord, error) {
	if ownerID == "" || categoryID == "" {
		return nil, nil
	}
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select `+budgetColumns+` from budgets.budgets where owner_id = $1 and category_id = $2 and deleted_at is null`, ownerID, categoryID)
	if err != nil {
		return nil, fmt.Errorf("list budgets for category: %w", err)
	}
	defer rows.Close()
	budgets := make([]domain.BudgetRecord, 0)
	for rows.Next() {
		budget, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		budgets = append(budgets, budget)
	}
	return budgets, rows.Err()
}

// GetBudget returns one live budget without owner scoping.
func (r *BudgetUsageRepository) GetBudget(ctx context.Context, budgetID string) (domain.BudgetRecord, bool, error) {
	budget, err := scanBudget(pgxtx.From(ctx, r.pool).QueryRow(ctx, `select `+budgetColumns+` from budgets.budgets where id = $1 and deleted_at is null`, budgetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BudgetRecord{}, false, nil
	}
	if err != nil {
		return domain.BudgetRecord{}, false, fmt.Errorf("load budget: %w", err)
	}
	return budget, true, nil
}

// SumContributions totals active contributions in the half-open window [from, to).
func (r *BudgetUsageRepository) SumContributions(ctx context.Context, ownerID, categoryID string, from, to time.Time) (int64, error) {
	var total int64
	if err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `
		select coalesce(sum(amount_minor), 0) from budgets.expense_contributions
		 where owner_id = $1 and category_id = $2 and active
		   and occurred_at >= $3 and occurred_at < $4`, ownerID, categoryID, from, to).Scan(&total); err != nil {
		return 0, fmt.Errorf("sum budget contributions: %w", err)
	}
	return total, nil
}

// LoadUsage returns the stored usage row for a budget period.
func (r *BudgetUsageRepository) LoadUsage(ctx context.Context, budgetID string, periodStart time.Time) (domain.BudgetUsage, bool, error) {
	usage := domain.BudgetUsage{BudgetID: budgetID, PeriodStart: periodStart}
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select spent_minor, alerted_percent, updated_at from budgets.budget_usage where budget_id = $1 and period_start = $2`, budgetID, periodStart).
		Scan(&usage.SpentMinor, &usage.AlertedPercent, &usage.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BudgetUsage{BudgetID: budgetID, PeriodStart: periodStart}, false, nil
	}
	if err != nil {
		return domain.BudgetUsage{}, false, fmt.Errorf("load budget usage: %w", err)
	}
	return usage, true, nil
}

// SaveUsage writes the recomputed spend and alert level for a period.
func (r *BudgetUsageRepository) SaveUsage(ctx context.Context, usage domain.BudgetUsage) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into budgets.budget_usage (budget_id, period_start, spent_minor, alerted_percent, updated_at)
		values ($1, $2, $3, $4, now())
		on conflict (budget_id, period_start) do update
		   set spent_minor = excluded.spent_minor, alerted_percent = excluded.alerted_percent, updated_at = now()`,
		usage.BudgetID, usage.PeriodStart, usage.SpentMinor, usage.AlertedPercent)
	if err != nil {
		return fmt.Errorf("save budget usage: %w", err)
	}
	return nil
}

// DeleteUsage removes every usage row for a budget.
func (r *BudgetUsageRepository) DeleteUsage(ctx context.Context, budgetID string) error {
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `delete from budgets.budget_usage where budget_id = $1`, budgetID); err != nil {
		return fmt.Errorf("delete budget usage: %w", err)
	}
	return nil
}
