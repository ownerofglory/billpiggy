package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/email"
	"github.com/ownerofglory/billpiggy/pkg/emailtmpl"
	"github.com/ownerofglory/billpiggy/pkg/metrics"
)

// Retry policy for notification delivery: five attempts, doubling from one
// minute up to a one-hour cap, with a two-minute lease so a crashed worker's
// claim is reclaimed well before the next attempt would be due anyway.
const (
	notificationMaxAttempts = 5
	notificationBaseBackoff = time.Minute
	notificationMaxBackoff  = time.Hour
	notificationLeaseTTL    = 2 * time.Minute
	// notificationQuotaBackoff applies instead of the normal exponential
	// schedule when the shared send quota (e.g. a provider's free-tier
	// monthly cap) is exhausted. That is not the individual delivery's own
	// fault, so it retries on a long, fixed cadence rather than racing back
	// every couple of minutes — each reclaim counts as an attempt (Attempts
	// increments at claim time, not at send time), and the normal schedule
	// would burn through notificationMaxAttempts and permanently drop the
	// delivery long before a monthly quota window has any chance to reset.
	notificationQuotaBackoff = time.Hour
)

// notificationRetryDelay returns the backoff to apply after attempts failed deliveries.
func notificationRetryDelay(attempts int) time.Duration {
	delay := notificationBaseBackoff
	for i := 1; i < attempts && delay < notificationMaxBackoff; i++ {
		delay *= 2
	}
	if delay > notificationMaxBackoff {
		delay = notificationMaxBackoff
	}
	return delay
}

// EmailSender delivers an already-rendered notification email in both a
// plain-text and an HTML part.
type EmailSender interface {
	Send(ctx context.Context, to, subject, text, html string) error
}

// NotificationService queues user notifications without coupling to an email provider.
type NotificationService struct {
	repository outbound.NotificationRepository
	outcomes   *metrics.CounterVec
	now        func() time.Time
}

// NewNotificationService creates a notification service.
func NewNotificationService(repository outbound.NotificationRepository) (*NotificationService, error) {
	if repository == nil {
		return nil, errors.New("notification repository is required")
	}
	return &NotificationService{repository: repository, now: time.Now}, nil
}

// WithMetrics records every resolved delivery against outcomes, labeled by
// kind and outcome (sent, retried, dead_lettered).
func (s *NotificationService) WithMetrics(outcomes *metrics.CounterVec) *NotificationService {
	s.outcomes = outcomes
	return s
}

// Queue creates an asynchronous delivery request.
func (s *NotificationService) Queue(ctx context.Context, userID string, kind domain.NotificationKind, payload map[string]string) error {
	if userID == "" || kind == "" {
		return errors.New("notification recipient and kind are required")
	}
	return s.repository.QueueNotification(ctx, domain.NotificationDelivery{ID: uuid.NewString(), UserID: userID, Kind: kind, Payload: payload, CreatedAt: s.now(), Status: domain.NotificationPending})
}

// DeliverPending claims a bounded batch under workerID and resolves each
// claim to sent, retried with backoff, or dead-lettered once its attempts are
// exhausted.
func (s *NotificationService) DeliverPending(ctx context.Context, users outbound.IdentityRepository, sender EmailSender, workerID string, limit int) error {
	if users == nil || sender == nil {
		return errors.New("identity repository and email sender are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	deliveries, err := s.repository.ClaimNotifications(ctx, workerID, notificationLeaseTTL, limit)
	if err != nil {
		return err
	}
	quotaExceeded := false
	for _, delivery := range deliveries {
		if quotaExceeded {
			// The shared send quota was already exhausted earlier in this
			// same batch: every remaining claimed delivery would fail the
			// same way, so resolve it the same way — back to pending on the
			// long quota backoff — without spending a round trip trying to
			// actually send it. Left claimed instead, it would only be
			// reclaimed via lease expiry, which increments Attempts on every
			// reclaim and would eventually dead-letter it for a problem that
			// was never its own.
			if err := s.repository.MarkNotificationRetry(ctx, delivery.ID, s.now().Add(notificationQuotaBackoff), email.ErrSendLimitExceeded.Error()); err != nil {
				return err
			}
			s.recordOutcome(delivery.Kind, "quota_exceeded")
			continue
		}
		stop, err := s.deliverOne(ctx, users, sender, delivery)
		if err != nil {
			return err
		}
		quotaExceeded = stop
	}
	return nil
}

// deliverOne resolves one claimed delivery. stop reports whether the shared
// send quota was exhausted, in which case the caller should stop the batch:
// every other queued send would fail the same way this tick, so there is no
// point spending the round trips.
//
// A recipient who has opted out of this notification kind is treated as
// delivered: there is nothing to retry, and the user made an intentional
// choice rather than the send failing.
//
// A delivery with RecipientEmail set (an invitation, addressed to someone who
// is not a user yet) always sends: there is no account, and so no preference
// to honour.
func (s *NotificationService) deliverOne(ctx context.Context, users outbound.IdentityRepository, sender EmailSender, delivery domain.NotificationDelivery) (stop bool, err error) {
	to, resolveErr := s.resolveRecipient(ctx, users, delivery)
	var sendErr error
	switch {
	case resolveErr != nil:
		sendErr = resolveErr
	case to == "":
		s.recordOutcome(delivery.Kind, "sent")
		return false, s.repository.MarkNotificationSent(ctx, delivery.ID)
	default:
		rendered, renderErr := emailtmpl.Render(string(delivery.Kind), delivery.Payload)
		if renderErr != nil {
			sendErr = renderErr
		} else {
			sendErr = sender.Send(ctx, to, rendered.Subject, rendered.Text, rendered.HTML)
		}
	}
	if sendErr == nil {
		s.recordOutcome(delivery.Kind, "sent")
		return false, s.repository.MarkNotificationSent(ctx, delivery.ID)
	}
	if errors.Is(sendErr, email.ErrSendLimitExceeded) {
		s.recordOutcome(delivery.Kind, "quota_exceeded")
		return true, s.repository.MarkNotificationRetry(ctx, delivery.ID, s.now().Add(notificationQuotaBackoff), sendErr.Error())
	}
	if delivery.Attempts >= notificationMaxAttempts {
		s.recordOutcome(delivery.Kind, "dead_lettered")
		return false, s.repository.MarkNotificationDeadLettered(ctx, delivery.ID, sendErr.Error())
	}
	s.recordOutcome(delivery.Kind, "retried")
	return false, s.repository.MarkNotificationRetry(ctx, delivery.ID, s.now().Add(notificationRetryDelay(delivery.Attempts)), sendErr.Error())
}

// recordOutcome increments the optional outcomes counter. A no-op when
// WithMetrics was never called.
func (s *NotificationService) recordOutcome(kind domain.NotificationKind, outcome string) {
	if s.outcomes != nil {
		s.outcomes.WithLabelValues(string(kind), outcome).Inc()
	}
}

// resolveRecipient returns the address to send to, or an empty string when
// the recipient has opted out of this kind. An error means the recipient
// could not be resolved at all (e.g. a deleted user) and the delivery should
// be retried or dead-lettered like a send failure.
func (s *NotificationService) resolveRecipient(ctx context.Context, users outbound.IdentityRepository, delivery domain.NotificationDelivery) (string, error) {
	if delivery.RecipientEmail != "" {
		return delivery.RecipientEmail, nil
	}
	user, err := users.GetUserByID(ctx, delivery.UserID)
	if err != nil {
		return "", err
	}
	if !user.WantsNotification(delivery.Kind) {
		return "", nil
	}
	return user.Email, nil
}
