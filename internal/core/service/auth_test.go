package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestAuthServiceInvitationAndRefreshRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)

	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap super admin: %v", err)
	}
	admin, err := repository.GetUserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("get super admin: %v", err)
	}
	invitation, err := auth.Invite(ctx, admin, "member@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite member: %v", err)
	}
	user, err := auth.AcceptInvitation(ctx, invitation.RawToken, "member-password", "Member")
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	if user.Role != domain.RoleMember {
		t.Fatalf("user role = %q, want member", user.Role)
	}

	session, err := auth.Login(ctx, "member@example.com", "member-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rotated, err := auth.Refresh(ctx, session.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := auth.Refresh(ctx, session.RefreshToken); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("replay refresh error = %v, want unauthorized", err)
	}
	if _, err := auth.AuthenticateAccessToken(ctx, rotated.AccessToken); err != nil {
		t.Fatalf("authenticate access token: %v", err)
	}
}

func TestAuthServiceRejectsSelfRegistrationAndMemberAdminActions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	auth := newAuthService(t, repository)
	if err := auth.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap super admin: %v", err)
	}
	member := domain.AppUser{ID: "member", Role: domain.RoleMember}
	if _, err := auth.Invite(ctx, member, "person@example.com", domain.RoleMember); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("member invite error = %v, want forbidden", err)
	}
	if _, err := auth.AcceptInvitation(ctx, "not-an-invitation", "member-password", "Member"); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("self-registration error = %v, want unauthorized", err)
	}
}

func newAuthService(t *testing.T, repository *memory.IdentityRepository) *service.AuthService {
	t.Helper()
	auth, err := service.NewAuthService(repository, service.AuthConfig{
		JWTSecret: "01234567890123456789012345678901", Issuer: "billpiggy-test",
		BootstrapSuperAdminEmail: "owner@example.com", BootstrapSuperAdminPassword: "super-admin-password",
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return auth
}
