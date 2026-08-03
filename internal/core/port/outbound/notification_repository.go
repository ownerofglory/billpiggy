package outbound

import (
	"context"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// NotificationRepository stores asynchronous notification requests for delivery workers.
type NotificationRepository interface {
	QueueNotification(context.Context, domain.NotificationDelivery) error
}
