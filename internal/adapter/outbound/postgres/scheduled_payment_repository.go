package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// scheduledPaymentColumns is the projection every read selects, in the order
// scanScheduledPayment expects.
const scheduledPaymentColumns = `id::text,owner_id::text,title,amount_minor,currency,
	coalesce(category_id::text,''),coalesce(category_name,''),
	coalesce(tag_ids::text[],'{}'::text[]),coalesce(shared_group_id::text,''),
	frequency,custom_interval_days,start_date,end_date,next_due_at,last_posted_at,
	auto_post,reminder_days_before,paused,created_at,updated_at,deleted_at`

// scanScheduledPayment reads one scheduled payment row.
func scanScheduledPayment(row pgx.Row) (domain.ScheduledPayment, error) {
	var payment domain.ScheduledPayment
	var frequency string
	err := row.Scan(&payment.ID, &payment.OwnerID, &payment.Title, &payment.AmountMinor, &payment.Currency,
		&payment.CategoryID, &payment.CategoryName, &payment.TagIDs, &payment.SharedGroupID,
		&frequency, &payment.CustomIntervalDays, &payment.StartDate, &payment.EndDate, &payment.NextDueAt,
		&payment.LastPostedAt, &payment.AutoPost, &payment.ReminderDaysBefore, &payment.Paused,
		&payment.CreatedAt, &payment.UpdatedAt, &payment.DeletedAt)
	payment.Frequency = domain.PaymentFrequency(frequency)
	return payment, err
}

// tagArray returns tagIDs as a value the uuid[] column accepts. A nil slice
// encodes as SQL NULL, which the NOT NULL column rejects, and a payment
// without tags is the common case rather than an error.
func tagArray(tagIDs []string) []string {
	if tagIDs == nil {
		return []string{}
	}
	return tagIDs
}

// ScheduledPaymentRepository persists the scheduled payment projection and
// its posting ledger in PostgreSQL.
type ScheduledPaymentRepository struct{ pool *pgxpool.Pool }

// NewScheduledPaymentRepository creates a PostgreSQL scheduled payment adapter.
func NewScheduledPaymentRepository(pool *pgxpool.Pool) *ScheduledPaymentRepository {
	return &ScheduledPaymentRepository{pool: pool}
}

// CreateScheduledPayment records a new recurring payment.
func (r *ScheduledPaymentRepository) CreateScheduledPayment(ctx context.Context, p domain.ScheduledPayment) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`insert into payments.scheduled_payments
		 (id,owner_id,title,amount_minor,currency,category_id,category_name,tag_ids,shared_group_id,
		  frequency,custom_interval_days,start_date,end_date,next_due_at,auto_post,reminder_days_before,
		  paused,created_at,updated_at)
		 values($1,$2,$3,$4,$5,nullif($6,'')::uuid,nullif($7,''),$8::uuid[],nullif($9,'')::uuid,
		        $10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		p.ID, p.OwnerID, p.Title, p.AmountMinor, p.Currency, p.CategoryID, p.CategoryName, tagArray(p.TagIDs),
		p.SharedGroupID, p.Frequency, p.CustomIntervalDays, p.StartDate, p.EndDate, p.NextDueAt,
		p.AutoPost, p.ReminderDaysBefore, p.Paused, p.CreatedAt, p.UpdatedAt)
	return err
}

// ListScheduledPayments returns payments owned by or shared with the viewer.
func (r *ScheduledPaymentRepository) ListScheduledPayments(ctx context.Context, owner string, sharedGroupIDs []string) ([]domain.ScheduledPayment, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx,
		`select `+scheduledPaymentColumns+` from payments.scheduled_payments
		 where (owner_id=$1 or shared_group_id = any($2::uuid[])) and deleted_at is null
		 order by created_at desc`, owner, sharedGroupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScheduledPayments(rows)
}

// GetScheduledPayment returns a payment owned by or shared with the viewer.
func (r *ScheduledPaymentRepository) GetScheduledPayment(ctx context.Context, owner, id string, sharedGroupIDs []string) (domain.ScheduledPayment, error) {
	return scanScheduledPayment(pgxtx.From(ctx, r.pool).QueryRow(ctx,
		`select `+scheduledPaymentColumns+` from payments.scheduled_payments
		 where id=$1 and (owner_id=$2 or shared_group_id = any($3::uuid[])) and deleted_at is null`,
		id, owner, sharedGroupIDs))
}

// UpdateScheduledPayment replaces an owner-scoped payment's editable fields.
func (r *ScheduledPaymentRepository) UpdateScheduledPayment(ctx context.Context, p domain.ScheduledPayment) error {
	tag, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`update payments.scheduled_payments set
		   title=$3,amount_minor=$4,currency=$5,category_id=nullif($6,'')::uuid,category_name=nullif($7,''),
		   tag_ids=$8::uuid[],shared_group_id=nullif($9,'')::uuid,frequency=$10,custom_interval_days=$11,
		   start_date=$12,end_date=$13,next_due_at=$14,auto_post=$15,reminder_days_before=$16,
		   paused=$17,updated_at=$18
		 where id=$1 and owner_id=$2 and deleted_at is null`,
		p.ID, p.OwnerID, p.Title, p.AmountMinor, p.Currency, p.CategoryID, p.CategoryName, tagArray(p.TagIDs),
		p.SharedGroupID, p.Frequency, p.CustomIntervalDays, p.StartDate, p.EndDate, p.NextDueAt,
		p.AutoPost, p.ReminderDaysBefore, p.Paused, p.UpdatedAt)
	if err == nil && tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return err
}

// DeleteScheduledPayment soft-deletes an owner-scoped payment.
func (r *ScheduledPaymentRepository) DeleteScheduledPayment(ctx context.Context, owner, id string) error {
	tag, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`update payments.scheduled_payments set deleted_at=now() where id=$1 and owner_id=$2 and deleted_at is null`, id, owner)
	if err == nil && tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return err
}

// ListDueScheduledPayments returns active payments due at or before through,
// oldest occurrence first so a backlog drains in order.
func (r *ScheduledPaymentRepository) ListDueScheduledPayments(ctx context.Context, through time.Time, limit int) ([]domain.ScheduledPayment, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx,
		`select `+scheduledPaymentColumns+` from payments.scheduled_payments
		 where deleted_at is null and paused = false and next_due_at <= $1
		 order by next_due_at asc limit $2`, through, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScheduledPayments(rows)
}

// ClaimPosting records one handled occurrence. The primary key on
// (scheduled_payment_id, due_at, kind) is the sole coordination mechanism
// between replicas whose schedulers reach the same occurrence: whichever
// insert lands first wins, and the other observes ErrPostingExists.
func (r *ScheduledPaymentRepository) ClaimPosting(ctx context.Context, posting domain.ScheduledPaymentPosting) error {
	tag, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`insert into payments.scheduled_payment_postings(scheduled_payment_id,due_at,kind,expense_id,posted_at)
		 values($1,$2,$3,nullif($4,'')::uuid,$5)
		 on conflict (scheduled_payment_id, due_at, kind) do nothing`,
		posting.ScheduledPaymentID, posting.DueAt, posting.Kind, posting.ExpenseID, posting.PostedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return outbound.ErrPostingExists
	}
	return nil
}

// AdvanceSchedule moves a payment's cursor to its next occurrence.
func (r *ScheduledPaymentRepository) AdvanceSchedule(ctx context.Context, paymentID string, nextDueAt, lastPostedAt time.Time, paused bool) error {
	tag, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`update payments.scheduled_payments
		 set next_due_at=$2,last_posted_at=$3,paused=$4,updated_at=now()
		 where id=$1 and deleted_at is null`, paymentID, nextDueAt, lastPostedAt, paused)
	if err == nil && tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return err
}

func collectScheduledPayments(rows pgx.Rows) ([]domain.ScheduledPayment, error) {
	payments := []domain.ScheduledPayment{}
	for rows.Next() {
		payment, err := scanScheduledPayment(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}
	return payments, rows.Err()
}
