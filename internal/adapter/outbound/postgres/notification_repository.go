package postgres

import (
	"context"
	"encoding/json"

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
	_, err = pgxtx.From(ctx, r.pool).Exec(ctx, `insert into notifications.deliveries(id,user_id,kind,payload,status,created_at) values($1,$2,$3,$4,$5,$6)`, value.ID, value.UserID, value.Kind, payload, domain.NotificationPending, value.CreatedAt)
	return err
}

// ClaimNotifications locks a batch until it is marked sent or failed.
func (r *NotificationRepository) ClaimNotifications(ctx context.Context, limit int) ([]domain.NotificationDelivery, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `with claimed as (select id from notifications.deliveries where status='pending' order by created_at for update skip locked limit $1) update notifications.deliveries d set status='processing' from claimed where d.id=claimed.id returning d.id::text,d.user_id::text,d.kind,d.payload,d.created_at`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.NotificationDelivery{}
	for rows.Next() {
		var value domain.NotificationDelivery
		var payload []byte
		if err := rows.Scan(&value.ID, &value.UserID, &value.Kind, &payload, &value.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &value.Payload); err != nil {
			return nil, err
		}
		value.Status = domain.NotificationPending
		values = append(values, value)
	}
	return values, rows.Err()
}

// MarkNotificationSent records successful email handoff.
func (r *NotificationRepository) MarkNotificationSent(ctx context.Context, id string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update notifications.deliveries set status='sent',sent_at=now() where id=$1`, id)
	return err
}

// MarkNotificationFailed records a failed email handoff.
func (r *NotificationRepository) MarkNotificationFailed(ctx context.Context, id, reason string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update notifications.deliveries set status='failed',failure_reason=$2 where id=$1`, id, reason)
	return err
}
