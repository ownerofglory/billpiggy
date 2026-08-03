package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ExpenseProjector consumes expense events into analytics read models.
type ExpenseProjector struct{ pool *pgxpool.Pool }

// NewExpenseProjector creates a PostgreSQL outbox projector.
func NewExpenseProjector(pool *pgxpool.Pool) *ExpenseProjector { return &ExpenseProjector{pool: pool} }

// ProjectPending processes a bounded batch of unprocessed expense events atomically.
func (p *ExpenseProjector) ProjectPending(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select o.id::text,e.event_type,e.payload from events.outbox o join events.events e on e.id=o.event_id where o.processed_at is null and e.aggregate_type='expense' order by o.id for update skip locked limit $1`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var outboxID, eventType string
		var payload []byte
		if err := rows.Scan(&outboxID, &eventType, &payload); err != nil {
			return 0, err
		}
		if err := p.projectEvent(ctx, tx, eventType, payload); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `update events.outbox set processed_at=now() where id=$1`, outboxID); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func (p *ExpenseProjector) projectEvent(ctx context.Context, tx pgx.Tx, eventType string, payload []byte) error {
	var envelope struct {
		Expense   domain.ExpenseRecord `json:"expense"`
		ExpenseID string               `json:"expense_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	id := envelope.Expense.ID
	if id == "" {
		id = envelope.ExpenseID
	}
	if id == "" {
		return fmt.Errorf("expense event has no id")
	}
	previous, found, err := loadContribution(ctx, tx, id)
	if err != nil {
		return err
	}
	if found && previous.active {
		if err := adjustRollups(ctx, tx, previous, -1); err != nil {
			return err
		}
	}
	if eventType == "expense_removed" {
		if found {
			_, err = tx.Exec(ctx, `update analytics.expense_contributions set active=false,updated_at=now() where expense_id=$1`, id)
		}
		return err
	}
	if eventType != "expense_added" && eventType != "expense_updated" {
		return nil
	}
	next := contribution{expenseID: id, ownerID: envelope.Expense.OwnerID, categoryID: envelope.Expense.CategoryID, tagIDs: envelope.Expense.TagIDs, currency: envelope.Expense.Currency, amountMinor: envelope.Expense.AmountMinor, occurredAt: envelope.Expense.OccurredAt, active: true}
	if next.ownerID == "" || next.categoryID == "" || next.currency == "" || next.occurredAt.IsZero() {
		return fmt.Errorf("expense event is incomplete")
	}
	if err := adjustRollups(ctx, tx, next, 1); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into analytics.expense_contributions(expense_id,owner_id,category_id,tag_ids,currency,amount_minor,occurred_at,active,updated_at) values($1,$2,$3,$4,$5,$6,$7,true,now()) on conflict(expense_id) do update set owner_id=excluded.owner_id,category_id=excluded.category_id,tag_ids=excluded.tag_ids,currency=excluded.currency,amount_minor=excluded.amount_minor,occurred_at=excluded.occurred_at,active=true,updated_at=now()`, next.expenseID, next.ownerID, next.categoryID, next.tagIDs, next.currency, next.amountMinor, next.occurredAt)
	return err
}

type contribution struct {
	expenseID, ownerID, categoryID, currency string
	tagIDs                                   []string
	amountMinor                              int64
	occurredAt                               time.Time
	active                                   bool
}

func loadContribution(ctx context.Context, tx pgx.Tx, id string) (contribution, bool, error) {
	var value contribution
	err := tx.QueryRow(ctx, `select expense_id::text,owner_id::text,coalesce(category_id::text,''),tag_ids,currency,amount_minor,occurred_at,active from analytics.expense_contributions where expense_id=$1`, id).Scan(&value.expenseID, &value.ownerID, &value.categoryID, &value.tagIDs, &value.currency, &value.amountMinor, &value.occurredAt, &value.active)
	if err == pgx.ErrNoRows {
		return contribution{}, false, nil
	}
	return value, err == nil, err
}
func adjustRollups(ctx context.Context, tx pgx.Tx, value contribution, direction int64) error {
	for _, period := range []string{"day", "week", "month", "year"} {
		if _, err := tx.Exec(ctx, `insert into analytics.expense_rollups(owner_id,period_start,period_kind,category_id,currency,amount_minor,expense_count) values($1,date_trunc($2,$3)::date,$2,$4,$5,$6,$7) on conflict(owner_id,period_start,period_kind,category_id,currency) do update set amount_minor=analytics.expense_rollups.amount_minor+excluded.amount_minor,expense_count=analytics.expense_rollups.expense_count+excluded.expense_count`, value.ownerID, period, value.occurredAt, value.categoryID, value.currency, value.amountMinor*direction, direction); err != nil {
			return err
		}
		for _, tagID := range value.tagIDs {
			if _, err := tx.Exec(ctx, `insert into analytics.tag_expense_rollups(owner_id,period_start,period_kind,tag_id,currency,amount_minor,expense_count) values($1,date_trunc($2,$3)::date,$2,$4,$5,$6,$7) on conflict(owner_id,period_start,period_kind,tag_id,currency) do update set amount_minor=analytics.tag_expense_rollups.amount_minor+excluded.amount_minor,expense_count=analytics.tag_expense_rollups.expense_count+excluded.expense_count`, value.ownerID, period, value.occurredAt, tagID, value.currency, value.amountMinor*direction, direction); err != nil {
				return err
			}
		}
	}
	return nil
}
