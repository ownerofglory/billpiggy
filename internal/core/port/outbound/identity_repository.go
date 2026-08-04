package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// IdentityRepository persists authentication and invitation projections.
// Implementations must make refresh-token rotation and invitation acceptance atomic.
type IdentityRepository interface {
	CountSuperAdmins(ctx context.Context) (int, error)
	GetUserByID(ctx context.Context, id string) (domain.AppUser, error)
	GetUserByEmail(ctx context.Context, email string) (domain.AppUser, error)
	ListUsers(ctx context.Context) ([]domain.AppUser, error)
	CreateUser(ctx context.Context, user domain.AppUser) error
	UpdateUser(ctx context.Context, user domain.AppUser) error
	DeleteUser(ctx context.Context, userID string) error
	GetInvitationByTokenHash(ctx context.Context, tokenHash string) (domain.Invitation, error)
	CreateInvitation(ctx context.Context, invitation domain.Invitation) error
	AcceptInvitation(ctx context.Context, invitationID string, user domain.AppUser) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error)
	CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error
	RotateRefreshToken(ctx context.Context, oldTokenID string, replacement domain.RefreshToken) error
	RevokeRefreshToken(ctx context.Context, tokenID string) error
	// RevokeAllRefreshTokens revokes every live refresh token for userID, used
	// after a password change so a stolen or leaked refresh token stops
	// working immediately rather than surviving until it next expires.
	RevokeAllRefreshTokens(ctx context.Context, userID string) error
}
