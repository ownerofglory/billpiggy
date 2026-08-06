package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// hashResetToken reproduces AuthService's unexported tokenHash so a test in
// this external package can seed a PasswordReset row directly against a
// chosen raw token, the same way the invitation and refresh-token tests seed
// their own rows.
func hashResetToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func TestRequestPasswordResetQueuesEmailForExistingAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "https://app.example.com")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if err := auth.RequestPasswordReset(ctx, "OWNER@EXAMPLE.COM"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}

	deliveries := notifications.Deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	queued := deliveries[0]
	if queued.Kind != domain.NotificationPasswordReset {
		t.Fatalf("kind = %s, want password_reset", queued.Kind)
	}
	if queued.RecipientEmail != "owner@example.com" {
		t.Fatalf("recipient = %q, want owner@example.com", queued.RecipientEmail)
	}
	if queued.UserID != "" {
		t.Fatalf("user id = %q, want empty — addressed directly so preferences can't suppress it", queued.UserID)
	}
	token := queued.Payload["token"]
	if token == "" {
		t.Fatal("payload carried no raw token")
	}
	wantURL := "https://app.example.com/reset-password?token=" + token
	if queued.Payload["reset_url"] != wantURL {
		t.Fatalf("reset_url = %q, want %q", queued.Payload["reset_url"], wantURL)
	}
	if queued.Payload["expires_at"] == "" {
		t.Fatal("payload carried no expiry")
	}
}

func TestRequestPasswordResetWithoutPublicBaseURLOmitsLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	payload := notifications.Deliveries()[0].Payload
	if _, ok := payload["reset_url"]; ok {
		t.Fatalf("reset_url = %q, want absent without a configured PublicBaseURL", payload["reset_url"])
	}
	if payload["token"] == "" {
		t.Fatal("token must still be present so the email can show the raw code")
	}
}

// TestRequestPasswordResetDoesNotLeakAccountExistence is the property that
// matters most for this endpoint: an unknown email and a blocked account
// must both look exactly like success, with nothing queued, so a caller
// cannot use this flow to discover which emails have accounts.
func TestRequestPasswordResetDoesNotLeakAccountExistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")
	invited, err := auth.Invite(ctx, admin, "blocked@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	blocked, err := auth.AcceptInvitation(ctx, invited.RawToken, "blocked-password-1", "Blocked")
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	if _, err := auth.ManageUser(ctx, admin, blocked.ID, domain.RoleMember, true); err != nil {
		t.Fatalf("block user: %v", err)
	}
	notifications = memory.NewNotificationRepository() // drop invitation/access-changed emails queued above
	auth = newAuthServiceWithNotifications(t, repository, notifications, "")

	if err := auth.RequestPasswordReset(ctx, "no-such-account@example.com"); err != nil {
		t.Fatalf("unknown email: %v, want nil", err)
	}
	if err := auth.RequestPasswordReset(ctx, "blocked@example.com"); err != nil {
		t.Fatalf("blocked account: %v, want nil", err)
	}
	if len(notifications.Deliveries()) != 0 {
		t.Fatalf("deliveries = %d, want 0 for an unknown or blocked account", len(notifications.Deliveries()))
	}
}

func TestResetPasswordSucceedsAndRevokesExistingSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	session, err := auth.Login(ctx, "owner@example.com", "super-admin-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	token := notifications.Deliveries()[0].Payload["token"]

	if err := auth.ResetPassword(ctx, token, "a-brand-new-password"); err != nil {
		t.Fatalf("reset password: %v", err)
	}

	if _, err := auth.Login(ctx, "owner@example.com", "super-admin-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("login with old password = %v, want unauthorized", err)
	}
	if _, err := auth.Login(ctx, "owner@example.com", "a-brand-new-password"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if _, err := auth.Refresh(ctx, session.RefreshToken); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("refresh with pre-reset session = %v, want unauthorized — a reset must revoke existing sessions", err)
	}
}

func TestResetPasswordRejectsAnUnknownToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := auth.ResetPassword(ctx, "not-a-real-token", "a-brand-new-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("error = %v, want unauthorized", err)
	}
}

func TestResetPasswordRejectsAnExpiredToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")
	if err := repository.CreatePasswordReset(ctx, domain.PasswordReset{
		ID: "reset-expired", UserID: admin.ID, TokenHash: hashResetToken("expired-token"),
		ExpiresAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed expired reset: %v", err)
	}

	if err := auth.ResetPassword(ctx, "expired-token", "a-brand-new-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("error = %v, want unauthorized for an expired token", err)
	}
}

func TestResetPasswordCannotBeReplayed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	token := notifications.Deliveries()[0].Payload["token"]

	if err := auth.ResetPassword(ctx, token, "first-new-password"); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if err := auth.ResetPassword(ctx, token, "second-new-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("replay error = %v, want unauthorized", err)
	}
}

// TestResetPasswordRejectsShortPasswordWithoutConsumingTheToken confirms a
// caller who fails validation can simply retry with a longer password,
// rather than having burned their only reset link on a typo.
func TestResetPasswordRejectsShortPasswordWithoutConsumingTheToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	token := notifications.Deliveries()[0].Payload["token"]

	if err := auth.ResetPassword(ctx, token, "short"); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("error = %v, want conflict for a too-short password", err)
	}
	if err := auth.ResetPassword(ctx, token, "a-long-enough-password"); err != nil {
		t.Fatalf("retry with a valid password: %v, want the token to still be usable", err)
	}
}

// TestResetPasswordInvalidatesOtherPendingResetsForTheSameAccount is the
// cleanup half of the guarantee: using one reset link must close out every
// other outstanding one for that account, not just the one that was used.
func TestResetPasswordInvalidatesOtherPendingResetsForTheSameAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("second request: %v", err)
	}
	deliveries := notifications.Deliveries()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(deliveries))
	}
	firstToken, secondToken := deliveries[0].Payload["token"], deliveries[1].Payload["token"]

	if err := auth.ResetPassword(ctx, firstToken, "a-brand-new-password"); err != nil {
		t.Fatalf("reset with the first token: %v", err)
	}
	if err := auth.ResetPassword(ctx, secondToken, "another-new-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("reset with the still-unused second token = %v, want unauthorized once the first has been used", err)
	}
}

// TestChangePasswordInvalidatesAPendingPasswordReset covers the other
// direction: a user who requested a reset link but then just changed their
// password normally must not leave that link usable by someone else who
// intercepted the email afterward.
func TestChangePasswordInvalidatesAPendingPasswordReset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	notifications := memory.NewNotificationRepository()
	auth := newAuthServiceWithNotifications(t, repository, notifications, "")
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, _ := repository.GetUserByEmail(ctx, "owner@example.com")

	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	token := notifications.Deliveries()[0].Payload["token"]

	if err := auth.ChangePassword(ctx, admin.ID, "super-admin-password", "a-changed-password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := auth.ResetPassword(ctx, token, "attacker-chosen-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("reset with the pre-change token = %v, want unauthorized", err)
	}
}

func TestResetPasswordRejectsABlockedAccount(t *testing.T) {
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
	member, err := auth.AcceptInvitation(ctx, invited.RawToken, "member-password-1", "Member")
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}

	if err := auth.RequestPasswordReset(ctx, "member@example.com"); err != nil {
		t.Fatalf("request password reset: %v", err)
	}
	deliveries := notifications.Deliveries()
	token := deliveries[len(deliveries)-1].Payload["token"]

	if _, err := auth.ManageUser(ctx, admin, member.ID, domain.RoleMember, true); err != nil {
		t.Fatalf("block member: %v", err)
	}
	if err := auth.ResetPassword(ctx, token, "a-brand-new-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("reset for a since-blocked account = %v, want unauthorized", err)
	}
}

func TestRequestPasswordResetWithoutNotificationsWiredStillSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := auth.RequestPasswordReset(ctx, "owner@example.com"); err != nil {
		t.Fatalf("request password reset without notifications wired should still succeed: %v", err)
	}
}
