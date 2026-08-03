package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// ExpenseRepository persists the expenses projection and receipt line items.
type ExpenseRepository struct{ pool *pgxpool.Pool }

// NewExpenseRepository creates an expense-projection adapter.
func NewExpenseRepository(pool *pgxpool.Pool) *ExpenseRepository {
	return &ExpenseRepository{pool: pool}
}

func (r *ExpenseRepository) CreateExpense(ctx context.Context, expense domain.ExpenseRecord) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertExpense(ctx, tx, expense); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ExpenseRepository) UpdateExpense(ctx context.Context, expense domain.ExpenseRecord) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `update expenses.expenses set title = $3, amount_minor = $4, currency = $5, occurred_at = $6, category_id = nullif($7, '')::uuid, status = $8, shared_group_id = nullif($9, '')::uuid, latitude = $10, longitude = $11, address = $12, receipt_object_key = $13, updated_at = $14 where id = $1 and owner_id = $2 and deleted_at is null`, expense.ID, expense.OwnerID, expense.Title, expense.AmountMinor, expense.Currency, expense.OccurredAt, expense.CategoryID, expense.Status, expense.SharedGroupID, expense.Latitude, expense.Longitude, expense.Address, expense.ReceiptObjectKey, expense.UpdatedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `delete from expenses.expense_tags where expense_id = $1; delete from expenses.expense_items where expense_id = $1`, expense.ID); err != nil {
		return err
	}
	if err := insertExpenseRelations(ctx, tx, expense); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ExpenseRepository) GetExpense(ctx context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error) {
	return r.loadExpense(ctx, r.pool, `select e.id::text, e.owner_id::text, e.title, e.amount_minor, e.currency, e.occurred_at, coalesce(e.category_id::text, ''), coalesce(c.name, ''), e.status::text, coalesce(e.shared_group_id::text, ''), e.latitude, e.longitude, coalesce(e.address, ''), coalesce(e.receipt_object_key, ''), e.created_at, e.updated_at from expenses.expenses e left join expenses.categories c on c.id = e.category_id where e.id = $1 and e.owner_id = $2 and e.deleted_at is null`, expenseID, ownerID)
}

func (r *ExpenseRepository) ListExpenses(ctx context.Context, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error) {
	query := `select e.id::text, e.owner_id::text, e.title, e.amount_minor, e.currency, e.occurred_at, coalesce(e.category_id::text, ''), coalesce(c.name, ''), e.status::text, coalesce(e.shared_group_id::text, ''), e.latitude, e.longitude, coalesce(e.address, ''), coalesce(e.receipt_object_key, ''), e.created_at, e.updated_at from expenses.expenses e left join expenses.categories c on c.id = e.category_id where e.owner_id = $1 and e.deleted_at is null`
	args := []any{filter.OwnerID}
	if filter.Query != "" {
		args = append(args, "%"+filter.Query+"%")
		query += fmt.Sprintf(" and (e.title ilike $%d or coalesce(c.name, '') ilike $%d)", len(args), len(args))
	}
	if filter.CategoryID != "" {
		args = append(args, filter.CategoryID)
		query += fmt.Sprintf(" and e.category_id = $%d", len(args))
	}
	args = append(args, filter.Limit, filter.Offset)
	query += fmt.Sprintf(" order by e.occurred_at desc limit $%d offset $%d", len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	expenses := make([]domain.ExpenseRecord, 0)
	for rows.Next() {
		expense, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		if err := r.loadRelations(ctx, r.pool, &expense); err != nil {
			return nil, err
		}
		if containsAllTags(expense.TagIDs, filter.TagIDs) {
			expenses = append(expenses, expense)
		}
	}
	return expenses, rows.Err()
}

func (r *ExpenseRepository) DeleteExpense(ctx context.Context, ownerID, expenseID string) error {
	command, err := r.pool.Exec(ctx, `update expenses.expenses set deleted_at = now(), updated_at = now() where id = $1 and owner_id = $2 and deleted_at is null`, expenseID, ownerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

type expenseQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *ExpenseRepository) loadExpense(ctx context.Context, query expenseQuerier, statement string, arguments ...any) (domain.ExpenseRecord, error) {
	expense, err := scanExpense(query.QueryRow(ctx, statement, arguments...))
	if err != nil {
		return domain.ExpenseRecord{}, err
	}
	if err := r.loadRelations(ctx, query, &expense); err != nil {
		return domain.ExpenseRecord{}, err
	}
	return expense, nil
}

func (r *ExpenseRepository) loadRelations(ctx context.Context, query expenseQuerier, expense *domain.ExpenseRecord) error {
	tags, err := query.Query(ctx, `select tag_id::text from expenses.expense_tags where expense_id = $1`, expense.ID)
	if err != nil {
		return err
	}
	for tags.Next() {
		var tagID string
		if err := tags.Scan(&tagID); err != nil {
			tags.Close()
			return err
		}
		expense.TagIDs = append(expense.TagIDs, tagID)
	}
	tags.Close()
	items, err := query.Query(ctx, `select title, quantity::text, amount_minor from expenses.expense_items where expense_id = $1 order by position`, expense.ID)
	if err != nil {
		return err
	}
	defer items.Close()
	for items.Next() {
		var item domain.ExpenseItem
		if err := items.Scan(&item.Title, &item.Quantity, &item.AmountMinor); err != nil {
			return err
		}
		expense.Items = append(expense.Items, item)
	}
	return items.Err()
}

func scanExpense(row pgx.Row) (domain.ExpenseRecord, error) {
	var expense domain.ExpenseRecord
	var status string
	err := row.Scan(&expense.ID, &expense.OwnerID, &expense.Title, &expense.AmountMinor, &expense.Currency, &expense.OccurredAt, &expense.CategoryID, &expense.CategoryName, &status, &expense.SharedGroupID, &expense.Latitude, &expense.Longitude, &expense.Address, &expense.ReceiptObjectKey, &expense.CreatedAt, &expense.UpdatedAt)
	expense.Status = domain.ExpenseStatus(status)
	return expense, err
}

func insertExpense(ctx context.Context, tx pgx.Tx, expense domain.ExpenseRecord) error {
	_, err := tx.Exec(ctx, `insert into expenses.expenses (id, owner_id, title, amount_minor, currency, occurred_at, category_id, status, shared_group_id, latitude, longitude, address, receipt_object_key, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, $8, nullif($9, '')::uuid, $10, $11, $12, $13, $14, $15)`, expense.ID, expense.OwnerID, expense.Title, expense.AmountMinor, expense.Currency, expense.OccurredAt, expense.CategoryID, expense.Status, expense.SharedGroupID, expense.Latitude, expense.Longitude, expense.Address, expense.ReceiptObjectKey, expense.CreatedAt, expense.UpdatedAt)
	if err != nil {
		return err
	}
	return insertExpenseRelations(ctx, tx, expense)
}

func insertExpenseRelations(ctx context.Context, tx pgx.Tx, expense domain.ExpenseRecord) error {
	for _, tagID := range expense.TagIDs {
		if _, err := tx.Exec(ctx, `insert into expenses.expense_tags (expense_id, tag_id) values ($1, $2)`, expense.ID, tagID); err != nil {
			return err
		}
	}
	for position, item := range expense.Items {
		if _, err := tx.Exec(ctx, `insert into expenses.expense_items (id, expense_id, title, quantity, amount_minor, position) values (gen_random_uuid(), $1, $2, $3, $4, $5)`, expense.ID, item.Title, item.Quantity, item.AmountMinor, position); err != nil {
			return err
		}
	}
	return nil
}

func containsAllTags(actual, required []string) bool {
	for _, wanted := range required {
		if !containsTag(actual, wanted) {
			return false
		}
	}
	return true
}
func containsTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, wanted) {
			return true
		}
	}
	return false
}
