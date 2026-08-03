package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// BudgetRepository persists the budget projection in PostgreSQL.
type BudgetRepository struct{ pool *pgxpool.Pool }

// NewBudgetRepository creates a PostgreSQL budget adapter.
func NewBudgetRepository(pool *pgxpool.Pool) *BudgetRepository { return &BudgetRepository{pool: pool} }
func (r *BudgetRepository) CreateBudget(ctx context.Context, b domain.BudgetRecord) error {
	_, e := r.pool.Exec(ctx, `insert into budgets.budgets(id,owner_id,category_id,name,amount_limit_minor,currency,threshold_percent,due_at,period,shared_group_id,created_at,updated_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,nullif($10,'')::uuid,$11,$12)`, b.ID, b.OwnerID, b.CategoryID, b.Name, b.AmountLimitMinor, b.Currency, b.ThresholdPercent, b.DueAt, b.Period, b.SharedGroupID, b.CreatedAt, b.UpdatedAt)
	return e
}
func (r *BudgetRepository) ListBudgets(ctx context.Context, owner string) ([]domain.BudgetRecord, error) {
	rows, e := r.pool.Query(ctx, `select id::text,owner_id::text,category_id::text,name,amount_limit_minor,currency,threshold_percent,due_at,period,coalesce(shared_group_id::text,''),created_at,updated_at from budgets.budgets where owner_id=$1 and deleted_at is null order by created_at desc`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.BudgetRecord{}
	for rows.Next() {
		var b domain.BudgetRecord
		var p string
		if e = rows.Scan(&b.ID, &b.OwnerID, &b.CategoryID, &b.Name, &b.AmountLimitMinor, &b.Currency, &b.ThresholdPercent, &b.DueAt, &p, &b.SharedGroupID, &b.CreatedAt, &b.UpdatedAt); e != nil {
			return nil, e
		}
		b.Period = domain.BudgetPeriod(p)
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *BudgetRepository) GetBudget(ctx context.Context, owner, id string) (domain.BudgetRecord, error) {
	var b domain.BudgetRecord
	var p string
	e := r.pool.QueryRow(ctx, `select id::text,owner_id::text,category_id::text,name,amount_limit_minor,currency,threshold_percent,due_at,period,coalesce(shared_group_id::text,''),created_at,updated_at from budgets.budgets where id=$1 and owner_id=$2 and deleted_at is null`, id, owner).Scan(&b.ID, &b.OwnerID, &b.CategoryID, &b.Name, &b.AmountLimitMinor, &b.Currency, &b.ThresholdPercent, &b.DueAt, &p, &b.SharedGroupID, &b.CreatedAt, &b.UpdatedAt)
	b.Period = domain.BudgetPeriod(p)
	return b, e
}
func (r *BudgetRepository) UpdateBudget(ctx context.Context, b domain.BudgetRecord) error {
	c, e := r.pool.Exec(ctx, `update budgets.budgets set name=$3,amount_limit_minor=$4,currency=$5,threshold_percent=$6,due_at=$7,period=$8,shared_group_id=nullif($9,'')::uuid,updated_at=$10 where id=$1 and owner_id=$2 and deleted_at is null`, b.ID, b.OwnerID, b.Name, b.AmountLimitMinor, b.Currency, b.ThresholdPercent, b.DueAt, b.Period, b.SharedGroupID, b.UpdatedAt)
	if e == nil && c.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return e
}
func (r *BudgetRepository) DeleteBudget(ctx context.Context, owner, id string) error {
	c, e := r.pool.Exec(ctx, `update budgets.budgets set deleted_at=now() where id=$1 and owner_id=$2 and deleted_at is null`, id, owner)
	if e == nil && c.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return e
}
