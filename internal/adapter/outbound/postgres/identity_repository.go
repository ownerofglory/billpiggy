package postgres

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// IdentityRepository stores user, invitation, and refresh-token projections in PostgreSQL.
type IdentityRepository struct{ pool *pgxpool.Pool }

// NewIdentityRepository creates an identity adapter from an existing connection pool.
func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{pool: pool}
}

// Ping is suitable for the readiness registry.
func (r *IdentityRepository) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

func (r *IdentityRepository) CountSuperAdmins(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `select count(*) from identity.users where role = 'super_admin' and access_blocked = false and deleted_at is null`).Scan(&count)
	return count, err
}

func (r *IdentityRepository) GetUserByID(ctx context.Context, id string) (domain.AppUser, error) {
	return r.getUser(ctx, `select id::text, email, password_hash, display_name, role::text, access_blocked, created_at, updated_at from identity.users where id = $1 and deleted_at is null`, id)
}

func (r *IdentityRepository) GetUserByEmail(ctx context.Context, email string) (domain.AppUser, error) {
	return r.getUser(ctx, `select id::text, email, password_hash, display_name, role::text, access_blocked, created_at, updated_at from identity.users where email = $1 and deleted_at is null`, email)
}

func (r *IdentityRepository) getUser(ctx context.Context, query string, argument any) (domain.AppUser, error) {
	var user domain.AppUser
	var role string
	err := r.pool.QueryRow(ctx, query, argument).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName, &role, &user.AccessBlocked, &user.CreatedAt, &user.UpdatedAt)
	user.Role = domain.UserRole(role)
	return user, err
}

func (r *IdentityRepository) CreateUser(ctx context.Context, user domain.AppUser) error {
	_, err := r.pool.Exec(ctx, `insert into identity.users (id, email, password_hash, display_name, role, access_blocked, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, $7, $8)`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.Role, user.AccessBlocked, user.CreatedAt, user.UpdatedAt)
	return err
}

func (r *IdentityRepository) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (domain.Invitation, error) {
	var invitation domain.Invitation
	var role, status string
	var acceptedAt *time.Time
	err := r.pool.QueryRow(ctx, `select id::text, email, role::text, token_hash, status::text, invited_by::text, expires_at, created_at, coalesce(accepted_by::text, ''), accepted_at from identity.invitations where token_hash = $1`, hashBytes(tokenHash)).Scan(&invitation.ID, &invitation.Email, &role, &invitation.TokenHash, &status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.CreatedAt, &invitation.AcceptedBy, &acceptedAt)
	invitation.Role, invitation.Status, invitation.AcceptedAt = domain.UserRole(role), domain.InvitationStatus(status), acceptedAt
	return invitation, err
}

func (r *IdentityRepository) CreateInvitation(ctx context.Context, invitation domain.Invitation) error {
	_, err := r.pool.Exec(ctx, `insert into identity.invitations (id, email, role, token_hash, status, invited_by, expires_at, created_at) values ($1, $2, $3, $4, $5, $6, $7, $8)`, invitation.ID, invitation.Email, invitation.Role, hashBytes(invitation.TokenHash), invitation.Status, invitation.InvitedBy, invitation.ExpiresAt, invitation.CreatedAt)
	return err
}

func (r *IdentityRepository) AcceptInvitation(ctx context.Context, invitationID string, user domain.AppUser) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `update identity.invitations set status = 'accepted', accepted_by = $2, accepted_at = now() where id = $1 and status = 'pending' and expires_at > now()`, invitationID, user.ID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `insert into identity.users (id, email, password_hash, display_name, role, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, $7)`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.Role, user.CreatedAt, user.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *IdentityRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	var token domain.RefreshToken
	var hash []byte
	var revokedAt *time.Time
	err := r.pool.QueryRow(ctx, `select id::text, user_id::text, token_hash, family_id::text, expires_at, created_at, revoked_at, coalesce(replaced_by::text, '') from identity.refresh_tokens where token_hash = $1`, hashBytes(tokenHash)).Scan(&token.ID, &token.UserID, &hash, &token.FamilyID, &token.ExpiresAt, &token.CreatedAt, &revokedAt, &token.ReplacedBy)
	token.TokenHash, token.RevokedAt = hex.EncodeToString(hash), revokedAt
	return token, err
}

func (r *IdentityRepository) CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error {
	_, err := r.pool.Exec(ctx, `insert into identity.refresh_tokens (id, user_id, token_hash, family_id, expires_at, created_at) values ($1, $2, $3, $4, $5, $6)`, token.ID, token.UserID, hashBytes(token.TokenHash), token.FamilyID, token.ExpiresAt, token.CreatedAt)
	return err
}

func (r *IdentityRepository) RotateRefreshToken(ctx context.Context, oldTokenID string, replacement domain.RefreshToken) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var familyID string
	if err := tx.QueryRow(ctx, `update identity.refresh_tokens set revoked_at = now(), replaced_by = $2 where id = $1 and revoked_at is null returning family_id::text`, oldTokenID, replacement.ID).Scan(&familyID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into identity.refresh_tokens (id, user_id, token_hash, family_id, expires_at, created_at) values ($1, $2, $3, $4, $5, $6)`, replacement.ID, replacement.UserID, hashBytes(replacement.TokenHash), familyID, replacement.ExpiresAt, replacement.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *IdentityRepository) RevokeRefreshToken(ctx context.Context, tokenID string) error {
	_, err := r.pool.Exec(ctx, `update identity.refresh_tokens set revoked_at = now() where id = $1 and revoked_at is null`, tokenID)
	return err
}

func hashBytes(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(fmt.Sprintf("invalid token hash: %v", err))
	}
	return decoded
}
