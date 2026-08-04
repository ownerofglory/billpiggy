package memory

import (
	"context"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// storedDelivery tracks lease state the domain type has no need to expose.
type storedDelivery struct {
	domain.NotificationDelivery
	availableAt time.Time
	lockedAt    time.Time
}

// NotificationRepository is an in-memory durable-delivery substitute for tests.
type NotificationRepository struct {
	mu         sync.Mutex
	deliveries map[string]storedDelivery
}

// NewNotificationRepository creates an empty notification queue.
func NewNotificationRepository() *NotificationRepository {
	return &NotificationRepository{deliveries: map[string]storedDelivery{}}
}

// QueueNotification enqueues a delivery request, available immediately.
func (r *NotificationRepository) QueueNotification(_ context.Context, value domain.NotificationDelivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries[value.ID] = storedDelivery{NotificationDelivery: value, availableAt: value.CreatedAt}
	return nil
}

// ClaimNotifications returns pending-and-due deliveries, plus processing
// deliveries whose lease has expired.
func (r *NotificationRepository) ClaimNotifications(_ context.Context, workerID string, leaseTTL time.Duration, limit int) ([]domain.NotificationDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	values := []domain.NotificationDelivery{}
	for id, stored := range r.deliveries {
		if len(values) >= limit {
			break
		}
		due := stored.Status == domain.NotificationPending && !stored.availableAt.After(now)
		abandoned := stored.Status == domain.NotificationProcessing && now.Sub(stored.lockedAt) >= leaseTTL
		if !due && !abandoned {
			continue
		}
		stored.Status = domain.NotificationProcessing
		stored.Attempts++
		stored.lockedAt = now
		r.deliveries[id] = stored
		values = append(values, stored.NotificationDelivery)
	}
	return values, nil
}

// MarkNotificationSent marks the delivery successful and clears its payload.
func (r *NotificationRepository) MarkNotificationSent(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.deliveries[id]
	if !ok {
		return errNotFound
	}
	value.Status = domain.NotificationSent
	value.Payload = nil
	r.deliveries[id] = value
	return nil
}

// MarkNotificationRetry returns a delivery to pending for another attempt.
func (r *NotificationRepository) MarkNotificationRetry(_ context.Context, id string, availableAt time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.deliveries[id]
	if !ok {
		return errNotFound
	}
	value.Status = domain.NotificationPending
	value.availableAt = availableAt
	r.deliveries[id] = value
	return nil
}

// MarkNotificationDeadLettered permanently fails a delivery and clears its payload.
func (r *NotificationRepository) MarkNotificationDeadLettered(_ context.Context, id, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.deliveries[id]
	if !ok {
		return errNotFound
	}
	value.Status = domain.NotificationFailed
	value.Payload = nil
	r.deliveries[id] = value
	return nil
}

// Deliveries returns every queued delivery, for test assertions.
func (r *NotificationRepository) Deliveries() []domain.NotificationDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make([]domain.NotificationDelivery, 0, len(r.deliveries))
	for _, value := range r.deliveries {
		values = append(values, value.NotificationDelivery)
	}
	return values
}

// Snapshot copies the queue and returns a function restoring it.
func (r *NotificationRepository) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := make(map[string]storedDelivery, len(r.deliveries))
	for id, value := range r.deliveries {
		saved[id] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.deliveries = saved
	}
}
