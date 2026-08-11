package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/email"
)

// recordingSender is a fake EmailSender whose behaviour is scripted per call.
type recordingSender struct {
	fail  bool
	sends []string
}

func (s *recordingSender) Send(_ context.Context, to, subject, text, html string) error {
	s.sends = append(s.sends, to)
	if s.fail {
		return errors.New("smtp unavailable")
	}
	if text == "" || html == "" {
		return errors.New("expected both text and html parts")
	}
	return nil
}

func newNotificationHarness(t *testing.T) (*service.NotificationService, *memory.NotificationRepository, *memory.IdentityRepository) {
	t.Helper()
	repository := memory.NewNotificationRepository()
	identity := memory.NewIdentityRepository()
	notifications, err := service.NewNotificationService(repository)
	if err != nil {
		t.Fatalf("new notification service: %v", err)
	}
	return notifications, repository, identity
}

func TestDeliverPendingSendsAndMarksSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationReportReady, map[string]string{"period_kind": "week", "period_start": "2026-07-27T00:00:00Z"}); err != nil {
		t.Fatalf("queue: %v", err)
	}

	sender := &recordingSender{}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	if len(sender.sends) != 1 || sender.sends[0] != "user@example.com" {
		t.Fatalf("sends = %#v, want one send to user@example.com", sender.sends)
	}
	deliveries := repository.Deliveries()
	if len(deliveries) != 1 || deliveries[0].Status != domain.NotificationSent {
		t.Fatalf("delivery = %#v, want status sent", deliveries)
	}
	if deliveries[0].Payload != nil {
		t.Fatalf("payload = %#v, want cleared after sending", deliveries[0].Payload)
	}
}

func TestDeliverPendingSkipsOptedOutRecipientWithoutSending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: false, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationBudgetAlert, map[string]string{}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	sender := &recordingSender{}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %#v, want none for an opted-out recipient", sender.sends)
	}
	if repository.Deliveries()[0].Status != domain.NotificationSent {
		t.Fatalf("status = %s, want sent (nothing to retry for an intentional opt-out)", repository.Deliveries()[0].Status)
	}
}

func TestDeliverPendingHonoursPerKindPreferenceOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, _, identity := newNotificationHarness(t)
	// Master switch on, but this one kind explicitly muted.
	if err := identity.CreateUser(ctx, domain.AppUser{
		ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true,
		NotificationPreferences: map[domain.NotificationKind]bool{domain.NotificationBudgetAlert: false},
		CreatedAt:               time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationBudgetAlert, map[string]string{}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	sender := &recordingSender{}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %#v, want none: budget_alert is muted for this user", sender.sends)
	}
}

func TestDeliverPendingRetriesFailedSendWithBackoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationReportReady, map[string]string{"period_kind": "week", "period_start": "2026-07-27T00:00:00Z"}); err != nil {
		t.Fatalf("queue: %v", err)
	}

	sender := &recordingSender{fail: true}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	deliveries := repository.Deliveries()
	if len(deliveries) != 1 || deliveries[0].Status != domain.NotificationPending || deliveries[0].Attempts != 1 {
		t.Fatalf("delivery = %#v, want pending with one recorded attempt", deliveries)
	}

	// An immediate second tick must not re-send: the retry backoff schedules
	// this delivery for later, not for the very next poll.
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending (immediate retry): %v", err)
	}
	if len(sender.sends) != 1 {
		t.Fatalf("sends = %d, want exactly one attempt so far (backoff not yet elapsed)", len(sender.sends))
	}
}

func TestDeliverPendingDeadLettersAfterExhaustingRetries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationReportReady, map[string]string{}); err != nil {
		t.Fatalf("queue: %v", err)
	}

	// Drive the delivery's Attempts up directly through the repository,
	// rewinding available_at into the past each time so the next claim is
	// immediate — this is the only way to exercise "attempts exhausted"
	// deterministically without a real backoff wait or an injectable clock.
	id := repository.Deliveries()[0].ID
	for i := 0; i < 10; i++ {
		claimed, err := repository.ClaimNotifications(ctx, "worker-1", time.Minute, 10)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if len(claimed) == 0 {
			break
		}
		if err := repository.MarkNotificationRetry(ctx, id, time.Now().Add(-time.Hour), "seed"); err != nil {
			t.Fatalf("mark retry: %v", err)
		}
		if repository.Deliveries()[0].Status == domain.NotificationFailed {
			break
		}
	}

	sender := &recordingSender{fail: true}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	final := repository.Deliveries()[0]
	if final.Status != domain.NotificationFailed {
		t.Fatalf("status = %s, want failed (dead-lettered) after exhausting retries", final.Status)
	}
	if final.Payload != nil {
		t.Fatalf("payload = %#v, want cleared once dead-lettered", final.Payload)
	}
}

func TestDeliverPendingReclaimsExpiredLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationReportReady, map[string]string{}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	// Simulate a worker that claimed the delivery and then crashed: locked
	// but never resolved to sent/retry/dead-letter.
	if _, err := repository.ClaimNotifications(ctx, "crashed-worker", time.Hour, 10); err != nil {
		t.Fatalf("initial claim: %v", err)
	}
	if repository.Deliveries()[0].Status != domain.NotificationProcessing {
		t.Fatalf("status = %s, want processing after the simulated crash", repository.Deliveries()[0].Status)
	}

	// A near-zero lease TTL means the "crashed" claim above is immediately
	// eligible for reclaim by the next caller.
	claimed, err := repository.ClaimNotifications(ctx, "worker-2", time.Nanosecond, 10)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("reclaimed = %d, want the abandoned delivery to be reclaimable", len(claimed))
	}
	if claimed[0].Attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (original claim + reclaim)", claimed[0].Attempts)
	}
}

// quotaExceededSender simulates a provider whose shared send quota (e.g.
// MailerSend's free-tier monthly cap) has already been exhausted: every send
// fails the same way, regardless of recipient.
type quotaExceededSender struct{ sends []string }

func (s *quotaExceededSender) Send(_ context.Context, to, _, _, _ string) error {
	s.sends = append(s.sends, to)
	return email.ErrSendLimitExceeded
}

func TestDeliverPendingBacksOffWithoutDeadLetteringWhenTheSendQuotaIsExhausted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := notifications.Queue(ctx, "user-1", domain.NotificationReportReady, map[string]string{"period_kind": "week", "period_start": "2026-07-27T00:00:00Z"}); err != nil {
		t.Fatalf("queue: %v", err)
	}

	sender := &quotaExceededSender{}
	for i := 0; i < 10; i++ {
		if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
			t.Fatalf("deliver pending (tick %d): %v", i, err)
		}
	}
	delivery := repository.Deliveries()[0]
	if delivery.Status != domain.NotificationPending {
		t.Fatalf("status = %s, want still pending: quota exhaustion is not this delivery's own fault and must never dead-letter it", delivery.Status)
	}
	// Backing off on a long, fixed cadence — rather than the short
	// exponential schedule — is what keeps repeated quota-exceeded ticks
	// from silently burning through the attempt cap.
	if len(sender.sends) != 1 {
		t.Fatalf("provider saw %d send attempts, want exactly 1: the long quota backoff must suppress the other 9 ticks", len(sender.sends))
	}
}

func TestDeliverPendingLeavesTheRestOfTheBatchPendingOnceTheQuotaTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notifications, repository, identity := newNotificationHarness(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", EmailNotificationsEnabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := notifications.Queue(ctx, "user-1", domain.NotificationBudgetAlert, map[string]string{}); err != nil {
			t.Fatalf("queue %d: %v", i, err)
		}
	}

	sender := &quotaExceededSender{}
	if err := notifications.DeliverPending(ctx, identity, sender, "worker-1", 10); err != nil {
		t.Fatalf("deliver pending: %v", err)
	}
	// Only the first delivery should have reached the sender at all: the
	// rest of the batch is resolved without another round trip once the
	// quota is known to be exhausted.
	if len(sender.sends) != 1 {
		t.Fatalf("provider saw %d send attempts, want exactly 1", len(sender.sends))
	}
	for _, delivery := range repository.Deliveries() {
		if delivery.Status != domain.NotificationPending {
			t.Fatalf("delivery %s status = %s, want pending (none dead-lettered or left dangling in processing)", delivery.ID, delivery.Status)
		}
	}
}
