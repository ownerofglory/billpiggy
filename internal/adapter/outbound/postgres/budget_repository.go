package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// budgetColumns is the projection every budget read selects, in the order
// scanBudget expects.
const budgetColumns = `id::text,owner_id::text,coalesce(category_id::text,''),name,amount_limit_minor,currency,
	threshold_percent,due_at,period,coalesce(shared_group_id::text,''),created_at,updated_at`

// scanBudget reads one budget row.
func scanBudget(row pgx.Row) (domain.BudgetRecord, error) {
	var budget domain.BudgetRecord
	var period string
	err := row.Scan(&budget.ID, &budget.OwnerID, &budget.CategoryID, &budget.Name, &budget.AmountLimitMinor,
		&budget.Currency, &budget.ThresholdPercent, &budget.DueAt, &period, &budget.SharedGroupID,
		&budget.CreatedAt, &budget.UpdatedAt)
	budget.Period = domain.BudgetPeriod(period)
	return budget, err
}

// BudgetRepository persists the budget projection in PostgreSQL.
type BudgetRepository struct{ pool *pgxpool.Pool }

// NewBudgetRepository creates a PostgreSQL budget adapter.
func NewBudgetRepository(pool *pgxpool.Pool) *BudgetRepository { return &BudgetRepository{pool: pool} }
func (r *BudgetRepository) CreateBudget(ctx context.Context, b domain.BudgetRecord) error {
	_, e := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into budgets.budgets(id,owner_id,category_id,name,amount_limit_minor,currency,threshold_percent,due_at,period,shared_group_id,created_at,updated_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,nullif($10,'')::uuid,$11,$12)`, b.ID, b.OwnerID, b.CategoryID, b.Name, b.AmountLimitMinor, b.Currency, b.ThresholdPercent, b.DueAt, b.Period, b.SharedGroupID, b.CreatedAt, b.UpdatedAt)
	return e
}

// ListBudgets returns budgets owned by or shared with the viewer.
func (r *BudgetRepository) ListBudgets(ctx context.Context, owner string, sharedGroupIDs []string) ([]domain.BudgetRecord, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select `+budgetColumns+` from budgets.budgets where (owner_id=$1 or shared_group_id = any($2::uuid[])) and deleted_at is null order by created_at desc`, owner, sharedGroupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	budgets := []domain.BudgetRecord{}
	for rows.Next() {
		budget, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		budgets = append(budgets, budget)
	}
	return budgets, rows.Err()
}

// GetBudget returns a budget owned by or shared with the viewer.
func (r *BudgetRepository) GetBudget(ctx context.Context, owner, id string, sharedGroupIDs []string) (domain.BudgetRecord, error) {
	return scanBudget(pgxtx.From(ctx, r.pool).QueryRow(ctx, `select `+budgetColumns+` from budgets.budgets where id=$1 and (owner_id=$2 or shared_group_id = any($3::uuid[])) and deleted_at is null`, id, owner, sharedGroupIDs))
}
func (r *BudgetRepository) UpdateBudget(ctx context.Context, b domain.BudgetRecord) error {
	c, e := pgxtx.From(ctx, r.pool).Exec(ctx, `update budgets.budgets set category_id=$3,name=$4,amount_limit_minor=$5,currency=$6,threshold_percent=$7,due_at=$8,period=$9,shared_group_id=nullif($10,'')::uuid,updated_at=$11 where id=$1 and owner_id=$2 and deleted_at is null`, b.ID, b.OwnerID, b.CategoryID, b.Name, b.AmountLimitMinor, b.Currency, b.ThresholdPercent, b.DueAt, b.Period, b.SharedGroupID, b.UpdatedAt)
	if e == nil && c.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return e
}
func (r *BudgetRepository) DeleteBudget(ctx context.Context, owner, id string) error {
	c, e := pgxtx.From(ctx, r.pool).Exec(ctx, `update budgets.budgets set deleted_at=now() where id=$1 and owner_id=$2 and deleted_at is null`, id, owner)
	if e == nil && c.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return e
}
