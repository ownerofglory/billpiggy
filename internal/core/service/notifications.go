package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"time"
)

// NotificationService queues user notifications without coupling to an email provider.
type NotificationService struct {
	repository outbound.NotificationRepository
	now        func() time.Time
}

// NewNotificationService creates a notification service.
func NewNotificationService(repository outbound.NotificationRepository) (*NotificationService, error) {
	if repository == nil {
		return nil, errors.New("notification repository is required")
	}
	return &NotificationService{repository: repository, now: time.Now}, nil
}

// Queue creates an asynchronous delivery request.
func (s *NotificationService) Queue(ctx context.Context, userID string, kind domain.NotificationKind, payload map[string]string) error {
	if userID == "" || kind == "" {
		return errors.New("notification recipient and kind are required")
	}
	return s.repository.QueueNotification(ctx, domain.NotificationDelivery{ID: uuid.NewString(), UserID: userID, Kind: kind, Payload: payload, CreatedAt: s.now()})
}
