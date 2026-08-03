package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// EmailSender delivers an already-rendered notification email.
type EmailSender interface {
	Send(context.Context, string, string, string) error
}

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
	return s.repository.QueueNotification(ctx, domain.NotificationDelivery{ID: uuid.NewString(), UserID: userID, Kind: kind, Payload: payload, CreatedAt: s.now(), Status: domain.NotificationPending})
}

// DeliverPending sends a bounded batch and persists a terminal status for each claim.
func (s *NotificationService) DeliverPending(ctx context.Context, users outbound.IdentityRepository, sender EmailSender, limit int) error {
	if users == nil || sender == nil {
		return errors.New("identity repository and email sender are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	deliveries, err := s.repository.ClaimNotifications(ctx, limit)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		user, err := users.GetUserByID(ctx, delivery.UserID)
		if err == nil && user.EmailNotificationsEnabled {
			err = sender.Send(ctx, user.Email, string(delivery.Kind), notificationBody(delivery))
		}
		if err != nil {
			if markErr := s.repository.MarkNotificationFailed(ctx, delivery.ID, err.Error()); markErr != nil {
				return markErr
			}
			continue
		}
		if err := s.repository.MarkNotificationSent(ctx, delivery.ID); err != nil {
			return err
		}
	}
	return nil
}

func notificationBody(delivery domain.NotificationDelivery) string {
	return fmt.Sprintf("BillPiggy notification (%s): %v", delivery.Kind, delivery.Payload)
}
