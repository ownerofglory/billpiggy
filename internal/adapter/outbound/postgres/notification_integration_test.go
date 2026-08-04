//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestNotificationRepositoryQueueAndClaim(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewNotificationRepository(pool)
	owner := seedUser(t, pool, "notify-owner@example.test")
	ctx := context.Background()

	delivery := domain.NotificationDelivery{ID: uuid.NewString(), UserID: owner, Kind: domain.NotificationReportReady, Payload: map[string]string{"period_kind": "week"}, CreatedAt: time.Now()}
	if err := repository.QueueNotification(ctx, delivery); err != nil {
		t.Fatalf("queue: %v", err)
	}

	claimed, err := repository.ClaimNotifications(ctx, "worker-1", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != delivery.ID {
		t.Fatalf("claimed = %#v", claimed)
	}
	if claimed[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 after the first claim", claimed[0].Attempts)
	}
	if claimed[0].Payload["period_kind"] != "week" {
		t.Fatalf("payload = %#v, want the queued payload intact", claimed[0].Payload)
	}

	// A second claim must not re-select the same delivery: it is now
	// 'processing' with a lease far in the future.
	again, err := repository.ClaimNotifications(ctx, "worker-2", time.Minute, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim = %#v, want none while the lease is still held", again)
	}
}

func TestNotificationRepositoryQueueWithRecipientEmailOnly(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewNotificationRepository(pool)
	ctx := context.Background()

	// An invitation email has no user_id: the invitee is not a user yet.
	delivery := domain.NotificationDelivery{ID: uuid.NewString(), RecipientEmail: "invitee@example.test", Kind: domain.NotificationInvitation, Payload: map[string]string{"token": "abc123"}, CreatedAt: time.Now()}
	if err := repository.QueueNotification(ctx, delivery); err != nil {
		t.Fatalf("queue: %v", err)
	}
	claimed, err := repository.ClaimNotifications(ctx, "worker-1", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].RecipientEmail != "invitee@example.test" || claimed[0].UserID != "" {
		t.Fatalf("claimed = %#v", claimed)
	}
}

func TestNotificationRepositoryMarkSentClearsPayload(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewNotificationRepository(pool)
	owner := seedUser(t, pool, "notify-sent@example.test")
	ctx := context.Background()

	delivery := domain.NotificationDelivery{ID: uuid.NewString(), UserID: owner, Kind: domain.NotificationBudgetAlert, Payload: map[string]string{"budget_name": "Groceries"}, CreatedAt: time.Now()}
	if err := repository.QueueNotification(ctx, delivery); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := repository.ClaimNotifications(ctx, "worker-1", time.Minute, 10); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := repository.MarkNotificationSent(ctx, delivery.ID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	var status string
	var payload []byte
	if err := pool.QueryRow(ctx, `select status, payload from notifications.deliveries where id = $1`, delivery.ID).Scan(&status, &payload); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "sent" {
		t.Fatalf("status = %q, want sent", status)
	}
	if string(payload) != "{}" {
		t.Fatalf("payload = %s, want cleared to {}", payload)
	}
}

func TestNotificationRepositoryRetryReschedulesAndDeadLetterClearsPayload(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewNotificationRepository(pool)
	owner := seedUser(t, pool, "notify-retry@example.test")
	ctx := context.Background()

	delivery := domain.NotificationDelivery{ID: uuid.NewString(), UserID: owner, Kind: domain.NotificationBudgetAlert, Payload: map[string]string{"budget_name": "Groceries"}, CreatedAt: time.Now()}
	if err := repository.QueueNotification(ctx, delivery); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := repository.ClaimNotifications(ctx, "worker-1", time.Minute, 10); err != nil {
		t.Fatalf("claim: %v", err)
	}
	availableAt := time.Now().Add(time.Hour)
	if err := repository.MarkNotificationRetry(ctx, delivery.ID, availableAt, "smtp down"); err != nil {
		t.Fatalf("mark retry: %v", err)
	}

	// Not yet claimable: available_at is an hour out.
	claimed, err := repository.ClaimNotifications(ctx, "worker-2", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim after retry: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed = %#v, want none before available_at arrives", claimed)
	}

	if err := repository.MarkNotificationDeadLettered(ctx, delivery.ID, "exhausted retries"); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	var status string
	var payload []byte
	if err := pool.QueryRow(ctx, `select status, payload from notifications.deliveries where id = $1`, delivery.ID).Scan(&status, &payload); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "failed" || string(payload) != "{}" {
		t.Fatalf("status = %q payload = %s, want failed with cleared payload", status, payload)
	}
}

func TestNotificationRepositoryReclaimsExpiredLease(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewNotificationRepository(pool)
	owner := seedUser(t, pool, "notify-lease@example.test")
	ctx := context.Background()

	delivery := domain.NotificationDelivery{ID: uuid.NewString(), UserID: owner, Kind: domain.NotificationReportReady, Payload: map[string]string{}, CreatedAt: time.Now()}
	if err := repository.QueueNotification(ctx, delivery); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := repository.ClaimNotifications(ctx, "crashed-worker", time.Hour, 10); err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	// A near-zero lease means the row claimed above is immediately treated
	// as abandoned by whatever worker locked it.
	reclaimed, err := repository.ClaimNotifications(ctx, "worker-2", time.Nanosecond, 10)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].Attempts != 2 {
		t.Fatalf("reclaimed = %#v, want one delivery with attempts=2", reclaimed)
	}
}

func TestIdentityRepositoryNotificationPreferencesRoundTrip(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	ctx := context.Background()
	owner := seedUser(t, pool, "prefs@example.test")

	user, err := repository.GetUserByID(ctx, owner)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if len(user.NotificationPreferences) != 0 {
		t.Fatalf("preferences = %#v, want empty by default", user.NotificationPreferences)
	}

	user.NotificationPreferences = map[domain.NotificationKind]bool{domain.NotificationBudgetAlert: false, domain.NotificationReportReady: true}
	if err := repository.UpdateUser(ctx, user); err != nil {
		t.Fatalf("update user: %v", err)
	}
	reloaded, err := repository.GetUserByID(ctx, owner)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.NotificationPreferences[domain.NotificationBudgetAlert] || !reloaded.NotificationPreferences[domain.NotificationReportReady] {
		t.Fatalf("preferences = %#v, want the persisted overrides", reloaded.NotificationPreferences)
	}
}
