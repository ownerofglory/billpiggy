package outbound

import (
	"context"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// NotificationRepository stores asynchronous notification requests for delivery workers.
type NotificationRepository interface {
	QueueNotification(context.Context, domain.NotificationDelivery) error
	ClaimNotifications(context.Context, int) ([]domain.NotificationDelivery, error)
	MarkNotificationSent(context.Context, string) error
	MarkNotificationFailed(context.Context, string, string) error
}
