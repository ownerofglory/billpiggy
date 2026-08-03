package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
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
	rows, err := r.pool.Query(ctx, query, args...)
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
