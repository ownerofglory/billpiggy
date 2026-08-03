package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// expenseColumns is the projection every expense read selects, in the order
// scanExpense expects.
const expenseColumns = `e.id::text, e.owner_id::text, e.title, e.amount_minor, e.currency, e.occurred_at,
	coalesce(e.category_id::text, ''), coalesce(c.name, ''), e.status::text, coalesce(e.shared_group_id::text, ''),
	e.latitude, e.longitude, coalesce(e.address, ''), coalesce(e.receipt_object_key, ''), e.created_at, e.updated_at`

// ExpenseRepository persists the expenses projection and receipt line items.
type ExpenseRepository struct{ pool *pgxpool.Pool }

// NewExpenseRepository creates an expense-projection adapter.
func NewExpenseRepository(pool *pgxpool.Pool) *ExpenseRepository {
	return &ExpenseRepository{pool: pool}
}

// CreateExpense inserts an expense with its tags and line items, joining the
// caller's transaction when there is one.
func (r *ExpenseRepository) CreateExpense(ctx context.Context, expense domain.ExpenseRecord) error {
	return insertExpense(ctx, pgxtx.From(ctx, r.pool), expense)
}

// UpdateExpense replaces an owner-scoped expense and its relations.
func (r *ExpenseRepository) UpdateExpense(ctx context.Context, expense domain.ExpenseRecord) error {
	querier := pgxtx.From(ctx, r.pool)
	command, err := querier.Exec(ctx, `update expenses.expenses set title = $3, amount_minor = $4, currency = $5, occurred_at = $6, category_id = nullif($7, '')::uuid, status = $8, shared_group_id = nullif($9, '')::uuid, latitude = $10, longitude = $11, address = $12, receipt_object_key = $13, updated_at = $14 where id = $1 and owner_id = $2 and deleted_at is null`,
		expense.ID, expense.OwnerID, expense.Title, expense.AmountMinor, expense.Currency, expense.OccurredAt,
		expense.CategoryID, expense.Status, expense.SharedGroupID, expense.Latitude, expense.Longitude,
		expense.Address, expense.ReceiptObjectKey, expense.UpdatedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	// One statement per Exec: pgx uses the extended protocol, which rejects
	// several semicolon-separated commands in a single parameterised call.
	if _, err := querier.Exec(ctx, `delete from expenses.expense_tags where expense_id = $1`, expense.ID); err != nil {
		return err
	}
	if _, err := querier.Exec(ctx, `delete from expenses.expense_items where expense_id = $1`, expense.ID); err != nil {
		return err
	}
	return insertExpenseRelations(ctx, querier, expense)
}

// GetExpense returns one expense owned by the given user.
func (r *ExpenseRepository) GetExpense(ctx context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error) {
	querier := pgxtx.From(ctx, r.pool)
	expense, err := scanExpense(querier.QueryRow(ctx, `select `+expenseColumns+` from expenses.expenses e left join expenses.categories c on c.id = e.category_id where e.id = $1 and e.owner_id = $2 and e.deleted_at is null`, expenseID, ownerID))
	if err != nil {
		return domain.ExpenseRecord{}, err
	}
	expenses := []domain.ExpenseRecord{expense}
	if err := loadRelations(ctx, querier, expenses); err != nil {
		return domain.ExpenseRecord{}, err
	}
	return expenses[0], nil
}

// ListExpenses returns owner-scoped expenses matching the search and filters.
func (r *ExpenseRepository) ListExpenses(ctx context.Context, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error) {
	query := `select ` + expenseColumns + ` from expenses.expenses e left join expenses.categories c on c.id = e.category_id where e.owner_id = $1 and e.deleted_at is null`
	args := []any{filter.OwnerID}
	if filter.Query != "" {
		args = append(args, "%"+filter.Query+"%")
		query += fmt.Sprintf(" and (e.title ilike $%d or coalesce(c.name, '') ilike $%d)", len(args), len(args))
	}
	if filter.CategoryID != "" {
		args = append(args, filter.CategoryID)
		query += fmt.Sprintf(" and e.category_id = $%d", len(args))
	}
	// Tag filtering belongs in SQL: applying it in Go after LIMIT/OFFSET would
	// silently return short pages. An expense matches when it carries every
	// requested tag, so the match count must equal the requested count.
	if tagIDs := distinct(filter.TagIDs); len(tagIDs) > 0 {
		args = append(args, tagIDs)
		query += fmt.Sprintf(` and (select count(*) from expenses.expense_tags et where et.expense_id = e.id and et.tag_id = any($%d::uuid[])) = cardinality($%d::uuid[])`, len(args), len(args))
	}
	args = append(args, filter.Limit, filter.Offset)
	query += fmt.Sprintf(" order by e.occurred_at desc, e.id limit $%d offset $%d", len(args)-1, len(args))

	querier := pgxtx.From(ctx, r.pool)
	rows, err := querier.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	expenses := make([]domain.ExpenseRecord, 0)
	for rows.Next() {
		expense, err := scanExpense(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		expenses = append(expenses, expense)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Relations load after the cursor is closed. Issuing queries while rows are
	// still open fails on a single connection, which is exactly what happens
	// inside a caller-owned transaction.
	if err := loadRelations(ctx, querier, expenses); err != nil {
		return nil, err
	}
	return expenses, nil
}

// DeleteExpense soft-deletes an owner-scoped expense.
func (r *ExpenseRepository) DeleteExpense(ctx context.Context, ownerID, expenseID string) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update expenses.expenses set deleted_at = now(), updated_at = now() where id = $1 and owner_id = $2 and deleted_at is null`, expenseID, ownerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// loadRelations attaches tags and line items to already-scanned expenses using
// one query each, rather than a query per expense.
func loadRelations(ctx context.Context, querier pgxtx.Querier, expenses []domain.ExpenseRecord) error {
	if len(expenses) == 0 {
		return nil
	}
	ids := make([]string, 0, len(expenses))
	index := make(map[string]*domain.ExpenseRecord, len(expenses))
	for i := range expenses {
		ids = append(ids, expenses[i].ID)
		index[expenses[i].ID] = &expenses[i]
	}
	tags, err := querier.Query(ctx, `select expense_id::text, tag_id::text from expenses.expense_tags where expense_id = any($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	for tags.Next() {
		var expenseID, tagID string
		if err := tags.Scan(&expenseID, &tagID); err != nil {
			tags.Close()
			return err
		}
		if expense, ok := index[expenseID]; ok {
			expense.TagIDs = append(expense.TagIDs, tagID)
		}
	}
	tags.Close()
	if err := tags.Err(); err != nil {
		return err
	}
	items, err := querier.Query(ctx, `select expense_id::text, title, quantity::text, amount_minor from expenses.expense_items where expense_id = any($1::uuid[]) order by expense_id, position`, ids)
	if err != nil {
		return err
	}
	defer items.Close()
	for items.Next() {
		var expenseID string
		var item domain.ExpenseItem
		if err := items.Scan(&expenseID, &item.Title, &item.Quantity, &item.AmountMinor); err != nil {
			return err
		}
		if expense, ok := index[expenseID]; ok {
			expense.Items = append(expense.Items, item)
		}
	}
	return items.Err()
}

func scanExpense(row pgx.Row) (domain.ExpenseRecord, error) {
	var expense domain.ExpenseRecord
	var status string
	err := row.Scan(&expense.ID, &expense.OwnerID, &expense.Title, &expense.AmountMinor, &expense.Currency,
		&expense.OccurredAt, &expense.CategoryID, &expense.CategoryName, &status, &expense.SharedGroupID,
		&expense.Latitude, &expense.Longitude, &expense.Address, &expense.ReceiptObjectKey,
		&expense.CreatedAt, &expense.UpdatedAt)
	expense.Status = domain.ExpenseStatus(status)
	return expense, err
}

func insertExpense(ctx context.Context, querier pgxtx.Querier, expense domain.ExpenseRecord) error {
	_, err := querier.Exec(ctx, `insert into expenses.expenses (id, owner_id, title, amount_minor, currency, occurred_at, category_id, status, shared_group_id, latitude, longitude, address, receipt_object_key, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, $8, nullif($9, '')::uuid, $10, $11, $12, $13, $14, $15)`,
		expense.ID, expense.OwnerID, expense.Title, expense.AmountMinor, expense.Currency, expense.OccurredAt,
		expense.CategoryID, expense.Status, expense.SharedGroupID, expense.Latitude, expense.Longitude,
		expense.Address, expense.ReceiptObjectKey, expense.CreatedAt, expense.UpdatedAt)
	if err != nil {
		return err
	}
	return insertExpenseRelations(ctx, querier, expense)
}

func insertExpenseRelations(ctx context.Context, querier pgxtx.Querier, expense domain.ExpenseRecord) error {
	for _, tagID := range distinct(expense.TagIDs) {
		if _, err := querier.Exec(ctx, `insert into expenses.expense_tags (expense_id, tag_id) values ($1, $2)`, expense.ID, tagID); err != nil {
			return err
		}
	}
	for position, item := range expense.Items {
		quantity := item.Quantity
		if quantity == "" {
			quantity = "1"
		}
		if _, err := querier.Exec(ctx, `insert into expenses.expense_items (id, expense_id, title, quantity, amount_minor, position) values (gen_random_uuid(), $1, $2, $3::numeric, $4, $5)`, expense.ID, item.Title, quantity, item.AmountMinor, position); err != nil {
			return err
		}
	}
	return nil
}

// distinct removes duplicates while preserving order.
func distinct(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
