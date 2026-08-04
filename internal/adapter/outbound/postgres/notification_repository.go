package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// NotificationRepository persists asynchronous email-delivery state in PostgreSQL.
type NotificationRepository struct{ pool *pgxpool.Pool }

// NewNotificationRepository creates a notification adapter.
func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// QueueNotification writes a pending notification.
func (r *NotificationRepository) QueueNotification(ctx context.Context, value domain.NotificationDelivery) error {
	payload, err := json.Marshal(value.Payload)
	if err != nil {
		return err
	}
	_, err = pgxtx.From(ctx, r.pool).Exec(ctx, `insert into notifications.deliveries(id,user_id,recipient_email,kind,payload,status,created_at) values($1,nullif($2,'')::uuid,nullif($3,''),$4,$5,$6,$7)`,
		value.ID, value.UserID, value.RecipientEmail, value.Kind, payload, domain.NotificationPending, value.CreatedAt)
	return err
}

// ClaimNotifications locks pending-and-due deliveries, plus processing
// deliveries whose lease has expired, until marked sent, retried, or
// dead-lettered.
func (r *NotificationRepository) ClaimNotifications(ctx context.Context, workerID string, leaseTTL time.Duration, limit int) ([]domain.NotificationDelivery, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `
		with claimed as (
			select id from notifications.deliveries
			where (status = 'pending' and available_at <= now())
			   or (status = 'processing' and locked_at < now() - make_interval(secs => $1))
			order by available_at
			for update skip locked
			limit $2
		)
		update notifications.deliveries d
		set status = 'processing', attempts = d.attempts + 1, locked_at = now(), locked_by = $3
		from claimed
		where d.id = claimed.id
		returning d.id::text, coalesce(d.user_id::text,''), coalesce(d.recipient_email,''), d.kind, d.payload, d.created_at, d.attempts`,
		leaseTTL.Seconds(), limit, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.NotificationDelivery{}
	for rows.Next() {
		var value domain.NotificationDelivery
		var payload []byte
		if err := rows.Scan(&value.ID, &value.UserID, &value.RecipientEmail, &value.Kind, &payload, &value.CreatedAt, &value.Attempts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &value.Payload); err != nil {
			return nil, err
		}
		value.Status = domain.NotificationProcessing
		values = append(values, value)
	}
	return values, rows.Err()
}

// MarkNotificationSent records successful email handoff and clears the
// payload, which nothing reads once delivery has succeeded.
func (r *NotificationRepository) MarkNotificationSent(ctx context.Context, id string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update notifications.deliveries set status='sent',sent_at=now(),payload='{}'::jsonb where id=$1`, id)
	return err
}

// MarkNotificationRetry returns a delivery to pending for another attempt
// once availableAt arrives.
func (r *NotificationRepository) MarkNotificationRetry(ctx context.Context, id string, availableAt time.Time, reason string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update notifications.deliveries set status='pending',available_at=$2,locked_at=null,locked_by=null,failure_reason=$3 where id=$1`, id, availableAt, reason)
	return err
}

// MarkNotificationDeadLettered permanently fails a delivery and clears its
// payload.
func (r *NotificationRepository) MarkNotificationDeadLettered(ctx context.Context, id, reason string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update notifications.deliveries set status='failed',failure_reason=$2,payload='{}'::jsonb where id=$1`, id, reason)
	return err
}

// CountByStatus tallies deliveries by their current status.
func (r *NotificationRepository) CountByStatus(ctx context.Context) (map[domain.NotificationStatus]int, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select status, count(*) from notifications.deliveries group by status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[domain.NotificationStatus]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[domain.NotificationStatus(status)] = count
	}
	return counts, rows.Err()
}
