package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// AuthService is everything an HTTP handler needs from authentication,
// invitation, and user-management commands. Wiring-only methods
// (WithObjectReferences, WithNotifications, EnsureBootstrapSuperAdmin) are
// deliberately excluded: only cmd/billpiggy calls those, at startup.
type AuthService interface {
	Login(ctx context.Context, email, password string) (service.Session, error)
	Refresh(ctx context.Context, rawToken string) (service.Session, error)
	Logout(ctx context.Context, rawToken string) error
	Invite(ctx context.Context, actor domain.AppUser, email string, role domain.UserRole) (service.InvitationDelivery, error)
	AcceptInvitation(ctx context.Context, rawToken, password, displayName string) (domain.AppUser, error)
	AuthenticateAccessToken(ctx context.Context, rawToken string) (domain.AppUser, error)
	ListUsers(ctx context.Context, actor domain.AppUser) ([]domain.AppUser, error)
	UpdateProfile(ctx context.Context, userID, displayName, email string, notifications, aiEnabled bool) (domain.AppUser, error)
	UpdateNotificationPreferences(ctx context.Context, userID string, preferences map[domain.NotificationKind]bool) (domain.AppUser, error)
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error
	RequestPasswordReset(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, rawToken, newPassword string) error
	GetProfile(ctx context.Context, userID string) (domain.AppUser, error)
	UpdateProfileImage(ctx context.Context, userID, objectKey string) (domain.AppUser, error)
	ManageUser(ctx context.Context, actor domain.AppUser, userID string, role domain.UserRole, blocked bool) (domain.AppUser, error)
	DeleteUser(ctx context.Context, actor domain.AppUser, userID string) error
}
