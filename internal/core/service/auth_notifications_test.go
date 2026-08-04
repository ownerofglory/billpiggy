package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func newAuthServiceWithNotifications(t *testing.T, repository *memory.IdentityRepository, notifications *memory.NotificationRepository, publicBaseURL string) *service.AuthService {
	t.Helper()
	auth, err := service.NewAuthService(repository, service.AuthConfig{
		JWTSecret: "01234567890123456789012345678901", Issuer: "billpiggy-test",
		BootstrapSuperAdminEmail: "owner@example.com", BootstrapSuperAdminPassword: "super-admin-password",
		PublicBaseURL: publicBaseURL,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return auth.WithNotifications(notifications)
}

func TestInviteQueuesInvitationEmailWithAcceptLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "https://app.example.com")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, err := repository.GetUserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}

	delivery, err := auth.Invite(ctx, admin, "member@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}

	deliveries := notifications.Deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	queued := deliveries[0]
	if queued.Kind != domain.NotificationInvitation {
		t.Fatalf("kind = %s, want invitation", queued.Kind)
	}
	if queued.RecipientEmail != "member@example.com" {
		t.Fatalf("recipient = %q, want member@example.com (invitee has no user id yet)", queued.RecipientEmail)
	}
	if queued.UserID != "" {
		t.Fatalf("user id = %q, want empty for an invitation", queued.UserID)
	}
	if queued.Payload["token"] != delivery.RawToken {
		t.Fatalf("payload token = %q, want the raw token %q", queued.Payload["token"], delivery.RawToken)
	}
	wantURL := "https://app.example.com/accept-invitation?token=" + delivery.RawToken
	if queued.Payload["accept_url"] != wantURL {
		t.Fatalf("accept_url = %q, want %q", queued.Payload["accept_url"], wantURL)
	}
}

func TestInviteWithoutPublicBaseURLOmitsAcceptLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")

	if _, err := auth.Invite(ctx, admin, "member@example.com", domain.RoleMember); err != nil {
		t.Fatalf("invite: %v", err)
	}
	payload := notifications.Deliveries()[0].Payload
	if _, ok := payload["accept_url"]; ok {
		t.Fatalf("accept_url = %q, want absent without a configured PublicBaseURL", payload["accept_url"])
	}
	if strings.TrimSpace(payload["token"]) == "" {
		t.Fatal("token must still be present so the email can show the raw code")
	}
}

func TestInviteWithoutNotificationsWiredStillSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")
	if _, err := auth.Invite(ctx, admin, "member@example.com", domain.RoleMember); err != nil {
		t.Fatalf("invite without notifications wired should still succeed: %v", err)
	}
}

func TestManageUserQueuesAccessChangedOnlyOnActualChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")
	invited, err := auth.Invite(ctx, admin, "member@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	// Opts out of email in general — access_changed must ignore that, since
	// it is how a blocked user finds out why they lost access.
	member, err := auth.AcceptInvitation(ctx, invited.RawToken, "member-password", "Member")
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	member.EmailNotificationsEnabled = false
	if err := repository.UpdateUser(ctx, member); err != nil {
		t.Fatalf("seed opted-out preference: %v", err)
	}
	notifications = memory.NewNotificationRepository() // drop the invitation email queued above
	auth = newAuthServiceWithNotifications(t, repository, notifications, "")

	// No-op management call: same role, same access state.
	if _, err := auth.ManageUser(ctx, admin, member.ID, domain.RoleMember, false); err != nil {
		t.Fatalf("manage user (no-op): %v", err)
	}
	if len(notifications.Deliveries()) != 0 {
		t.Fatalf("deliveries = %d, want 0 for a no-op management call", len(notifications.Deliveries()))
	}

	// A real change (blocking) must queue access_changed regardless of the
	// user's own email preference.
	if _, err := auth.ManageUser(ctx, admin, member.ID, domain.RoleMember, true); err != nil {
		t.Fatalf("manage user (block): %v", err)
	}
	deliveries := notifications.Deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1 after blocking the user", len(deliveries))
	}
	if deliveries[0].Kind != domain.NotificationAccessChanged || deliveries[0].RecipientEmail != "member@example.com" {
		t.Fatalf("delivery = %#v", deliveries[0])
	}
	if deliveries[0].Payload["blocked"] != "true" {
		t.Fatalf("payload blocked = %q, want true", deliveries[0].Payload["blocked"])
	}
}

func TestUpdateNotificationPreferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")

	updated, err := auth.UpdateNotificationPreferences(ctx, admin.ID, map[domain.NotificationKind]bool{domain.NotificationBudgetAlert: false})
	if err != nil {
		t.Fatalf("update preferences: %v", err)
	}
	if updated.WantsNotification(domain.NotificationBudgetAlert) {
		t.Fatal("budget_alert should be muted after the override")
	}
	if !updated.WantsNotification(domain.NotificationReportReady) {
		t.Fatal("report_ready should still follow the master switch (default true)")
	}
	reloaded, err := repository.GetUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.NotificationPreferences[domain.NotificationBudgetAlert] {
		t.Fatal("preference override did not persist")
	}
}
