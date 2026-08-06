// Package cached wraps outbound adapters with transparent, explicitly
// invalidated caching for slow-changing reads: user lookups on the auth hot
// path, and taxonomy and group lists that change far less often than they
// are read. Every decorator delegates straight through for any method it
// does not itself cache, so wrapping an adapter never changes its behavior
// beyond adding a cache in front of specific reads.
package cached

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/cache"
)

// IdentityRepository caches GetUserByID, which AuthenticateAccessToken calls
// on every authenticated request — the single hottest read in the app.
// Every other method delegates straight through uncached.
type IdentityRepository struct {
	inner outbound.IdentityRepository
	users *cache.Cache[string, domain.AppUser]
}

// NewIdentityRepository wraps inner, caching GetUserByID results for ttl.
func NewIdentityRepository(inner outbound.IdentityRepository, ttl time.Duration) *IdentityRepository {
	return &IdentityRepository{inner: inner, users: cache.New[string, domain.AppUser](ttl)}
}

// GetUserByID returns the cached user when present, otherwise loads and
// caches it.
func (r *IdentityRepository) GetUserByID(ctx context.Context, id string) (domain.AppUser, error) {
	if user, ok := r.users.Get(id); ok {
		return user, nil
	}
	user, err := r.inner.GetUserByID(ctx, id)
	if err != nil {
		return domain.AppUser{}, err
	}
	r.users.Set(id, user)
	return user, nil
}

// UpdateUser writes through and invalidates the updated user's cache entry,
// so the next read observes the change rather than a stale cached copy.
func (r *IdentityRepository) UpdateUser(ctx context.Context, user domain.AppUser) error {
	if err := r.inner.UpdateUser(ctx, user); err != nil {
		return err
	}
	r.users.Invalidate(user.ID)
	return nil
}

// DeleteUser writes through and invalidates the deleted user's cache entry.
func (r *IdentityRepository) DeleteUser(ctx context.Context, userID string) error {
	if err := r.inner.DeleteUser(ctx, userID); err != nil {
		return err
	}
	r.users.Invalidate(userID)
	return nil
}

func (r *IdentityRepository) CountSuperAdmins(ctx context.Context) (int, error) {
	return r.inner.CountSuperAdmins(ctx)
}
func (r *IdentityRepository) GetUserByEmail(ctx context.Context, email string) (domain.AppUser, error) {
	return r.inner.GetUserByEmail(ctx, email)
}
func (r *IdentityRepository) ListUsers(ctx context.Context) ([]domain.AppUser, error) {
	return r.inner.ListUsers(ctx)
}
func (r *IdentityRepository) CreateUser(ctx context.Context, user domain.AppUser) error {
	return r.inner.CreateUser(ctx, user)
}
func (r *IdentityRepository) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (domain.Invitation, error) {
	return r.inner.GetInvitationByTokenHash(ctx, tokenHash)
}
func (r *IdentityRepository) CreateInvitation(ctx context.Context, invitation domain.Invitation) error {
	return r.inner.CreateInvitation(ctx, invitation)
}
func (r *IdentityRepository) AcceptInvitation(ctx context.Context, invitationID string, user domain.AppUser) error {
	return r.inner.AcceptInvitation(ctx, invitationID, user)
}
func (r *IdentityRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	return r.inner.GetRefreshTokenByHash(ctx, tokenHash)
}
func (r *IdentityRepository) CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error {
	return r.inner.CreateRefreshToken(ctx, token)
}
func (r *IdentityRepository) RotateRefreshToken(ctx context.Context, oldTokenID string, replacement domain.RefreshToken) error {
	return r.inner.RotateRefreshToken(ctx, oldTokenID, replacement)
}
func (r *IdentityRepository) RevokeRefreshToken(ctx context.Context, tokenID string) error {
	return r.inner.RevokeRefreshToken(ctx, tokenID)
}
func (r *IdentityRepository) RevokeAllRefreshTokens(ctx context.Context, userID string) error {
	return r.inner.RevokeAllRefreshTokens(ctx, userID)
}
func (r *IdentityRepository) GetPasswordResetByTokenHash(ctx context.Context, tokenHash string) (domain.PasswordReset, error) {
	return r.inner.GetPasswordResetByTokenHash(ctx, tokenHash)
}
func (r *IdentityRepository) CreatePasswordReset(ctx context.Context, reset domain.PasswordReset) error {
	return r.inner.CreatePasswordReset(ctx, reset)
}
func (r *IdentityRepository) MarkPasswordResetUsed(ctx context.Context, resetID string) error {
	return r.inner.MarkPasswordResetUsed(ctx, resetID)
}
func (r *IdentityRepository) InvalidatePendingPasswordResets(ctx context.Context, userID string) error {
	return r.inner.InvalidatePendingPasswordResets(ctx, userID)
}
