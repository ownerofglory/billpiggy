package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// NotificationRepository is an in-memory durable-delivery substitute for tests.
type NotificationRepository struct {
	mu         sync.Mutex
	deliveries map[string]domain.NotificationDelivery
}

// NewNotificationRepository creates an empty notification queue.
func NewNotificationRepository() *NotificationRepository {
	return &NotificationRepository{deliveries: map[string]domain.NotificationDelivery{}}
}

// QueueNotification enqueues a delivery request.
func (r *NotificationRepository) QueueNotification(_ context.Context, value domain.NotificationDelivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries[value.ID] = value
	return nil
}

// ClaimNotifications returns pending delivery requests.
func (r *NotificationRepository) ClaimNotifications(_ context.Context, limit int) ([]domain.NotificationDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := []domain.NotificationDelivery{}
	for _, value := range r.deliveries {
		if value.Status == domain.NotificationPending && len(values) < limit {
			values = append(values, value)
		}
	}
	return values, nil
}

// MarkNotificationSent marks the delivery successful.
func (r *NotificationRepository) MarkNotificationSent(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.deliveries[id]
	if !ok {
		return errNotFound
	}
	value.Status = domain.NotificationSent
	r.deliveries[id] = value
	return nil
}

// MarkNotificationFailed marks the delivery unsuccessful.
func (r *NotificationRepository) MarkNotificationFailed(_ context.Context, id, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.deliveries[id]
	if !ok {
		return errNotFound
	}
	value.Status = domain.NotificationFailed
	r.deliveries[id] = value
	return nil
}
