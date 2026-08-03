package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// AnalyticsRepository queries PostgreSQL analytics projections.
type AnalyticsRepository struct{ pool *pgxpool.Pool }

// NewAnalyticsRepository creates an analytics query adapter.
func NewAnalyticsRepository(pool *pgxpool.Pool) *AnalyticsRepository {
	return &AnalyticsRepository{pool: pool}
}

// ListExpenseRollups returns category or tag rollups for one owner.
func (r *AnalyticsRepository) ListExpenseRollups(ctx context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error) {
	table, dimension := "analytics.expense_rollups", "category_id"
	if len(filter.TagIDs) > 0 {
		table, dimension = "analytics.tag_expense_rollups", "tag_id"
	}
	query := fmt.Sprintf(`select period_start, %s::text, currency, amount_minor, expense_count from %s where owner_id=$1 and period_kind=$2 and period_start >= $3 and period_start <= $4`, dimension, table)
	args := []any{filter.OwnerID, filter.Period, filter.From, filter.To}
	if filter.CategoryID != "" && dimension == "category_id" {
		args = append(args, filter.CategoryID)
		query += fmt.Sprintf(" and category_id=$%d", len(args))
	}
	if len(filter.TagIDs) > 0 {
		args = append(args, filter.TagIDs)
		query += fmt.Sprintf(" and tag_id = any($%d)", len(args))
	}
	query += " order by period_start"
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ExpenseRollup{}
	for rows.Next() {
		var value domain.ExpenseRollup
		value.OwnerID = filter.OwnerID
		value.Period = filter.Period
		var dimensionID string
		if err := rows.Scan(&value.PeriodStart, &dimensionID, &value.Currency, &value.AmountMinor, &value.ExpenseCount); err != nil {
			return nil, err
		}
		if dimension == "tag_id" {
			value.TagID = dimensionID
		} else {
			value.CategoryID = dimensionID
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// LoadContribution returns the contribution currently reflected in the rollups.
func (r *AnalyticsRepository) LoadContribution(ctx context.Context, expenseID string) (domain.ExpenseContribution, bool, error) {
	var value domain.ExpenseContribution
	value.ExpenseID = expenseID
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select owner_id::text, coalesce(category_id::text, ''), tag_ids::text[], currency, amount_minor, occurred_at, active from analytics.expense_contributions where expense_id = $1`, expenseID).
		Scan(&value.OwnerID, &value.CategoryID, &value.TagIDs, &value.Currency, &value.AmountMinor, &value.OccurredAt, &value.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExpenseContribution{}, false, nil
	}
	if err != nil {
		return domain.ExpenseContribution{}, false, fmt.Errorf("load analytics contribution: %w", err)
	}
	return value, true, nil
}

// SaveContribution records the contribution now reflected in the rollups. A
// deactivated contribution is kept as a tombstone so a later redelivery of the
// removal event does not reverse the same amounts twice.
func (r *AnalyticsRepository) SaveContribution(ctx context.Context, contribution domain.ExpenseContribution) error {
	tagIDs := contribution.TagIDs
	if tagIDs == nil {
		tagIDs = []string{}
	}
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into analytics.expense_contributions
			(expense_id, owner_id, category_id, tag_ids, currency, amount_minor, occurred_at, active, updated_at)
		values ($1, $2, nullif($3, '')::uuid, $4::uuid[], $5, $6, $7, $8, now())
		on conflict (expense_id) do update
		   set owner_id = excluded.owner_id, category_id = excluded.category_id, tag_ids = excluded.tag_ids,
		       currency = excluded.currency, amount_minor = excluded.amount_minor,
		       occurred_at = excluded.occurred_at, active = excluded.active, updated_at = now()`,
		contribution.ExpenseID, contribution.OwnerID, contribution.CategoryID, tagIDs,
		contribution.Currency, contribution.AmountMinor, contribution.OccurredAt, contribution.Active)
	if err != nil {
		return fmt.Errorf("save analytics contribution: %w", err)
	}
	return nil
}

// AddRollupDelta applies a signed adjustment to one rollup bucket, creating the
// bucket when it does not exist yet.
func (r *AnalyticsRepository) AddRollupDelta(ctx context.Context, delta domain.RollupDelta) error {
	table, dimension, dimensionID := "analytics.expense_rollups", "category_id", delta.CategoryID
	if delta.TagID != "" {
		table, dimension, dimensionID = "analytics.tag_expense_rollups", "tag_id", delta.TagID
	}
	statement := fmt.Sprintf(`
		insert into %s (owner_id, period_start, period_kind, %s, currency, amount_minor, expense_count)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (owner_id, period_start, period_kind, %s, currency) do update
		   set amount_minor  = %s.amount_minor + excluded.amount_minor,
		       expense_count = %s.expense_count + excluded.expense_count`, table, dimension, dimension, table, table)
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, statement, delta.OwnerID, delta.PeriodStart, string(delta.Period), dimensionID, delta.Currency, delta.AmountMinor, delta.ExpenseCount); err != nil {
		return fmt.Errorf("apply rollup delta: %w", err)
	}
	return nil
}
