package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// NotificationRepository stores asynchronous notification requests for delivery workers.
type NotificationRepository interface {
	// QueueNotification writes a pending delivery, available immediately.
	QueueNotification(ctx context.Context, delivery domain.NotificationDelivery) error
	// ClaimNotifications locks up to limit deliveries for workerID: pending
	// deliveries whose available_at has arrived, plus processing deliveries
	// whose lease (locked at least leaseTTL ago) has expired, treated as
	// abandoned by whichever worker crashed mid-attempt. Each claimed
	// delivery's Attempts already reflects this attempt.
	ClaimNotifications(ctx context.Context, workerID string, leaseTTL time.Duration, limit int) ([]domain.NotificationDelivery, error)
	// MarkNotificationSent records successful delivery and clears the
	// payload, which nothing reads once a delivery reaches a terminal state.
	MarkNotificationSent(ctx context.Context, id string) error
	// MarkNotificationRetry returns a delivery to pending, available again at
	// availableAt, recording reason for operators inspecting the queue.
	MarkNotificationRetry(ctx context.Context, id string, availableAt time.Time, reason string) error
	// MarkNotificationDeadLettered permanently fails a delivery that has
	// exhausted its retry attempts, and clears its payload.
	MarkNotificationDeadLettered(ctx context.Context, id string, reason string) error
}
