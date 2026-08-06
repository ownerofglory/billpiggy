package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
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
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select count(*) from identity.users where role = 'super_admin' and access_blocked = false and deleted_at is null`).Scan(&count)
	return count, err
}

const userColumns = `id::text, email, password_hash, display_name, coalesce(profile_image_object_key, ''), role::text, access_blocked, email_notifications_enabled, notification_preferences, ai_enabled, created_at, updated_at`

func (r *IdentityRepository) GetUserByID(ctx context.Context, id string) (domain.AppUser, error) {
	return r.getUser(ctx, `select `+userColumns+` from identity.users where id = $1 and deleted_at is null`, id)
}

func (r *IdentityRepository) GetUserByEmail(ctx context.Context, email string) (domain.AppUser, error) {
	return r.getUser(ctx, `select `+userColumns+` from identity.users where email = $1 and deleted_at is null`, email)
}

func (r *IdentityRepository) getUser(ctx context.Context, query string, argument any) (domain.AppUser, error) {
	var user domain.AppUser
	var role string
	var preferences []byte
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, query, argument).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName, &user.ProfileImageObjectKey, &role, &user.AccessBlocked, &user.EmailNotificationsEnabled, &preferences, &user.AIEnabled, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.AppUser{}, err
	}
	user.Role = domain.UserRole(role)
	if err := unmarshalPreferences(preferences, &user.NotificationPreferences); err != nil {
		return domain.AppUser{}, err
	}
	return user, nil
}

// ListUsers returns all active user projections for administration.
func (r *IdentityRepository) ListUsers(ctx context.Context) ([]domain.AppUser, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select `+userColumns+` from identity.users where deleted_at is null order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.AppUser{}
	for rows.Next() {
		var user domain.AppUser
		var role string
		var preferences []byte
		if err := rows.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName, &user.ProfileImageObjectKey, &role, &user.AccessBlocked, &user.EmailNotificationsEnabled, &preferences, &user.AIEnabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		user.Role = domain.UserRole(role)
		if err := unmarshalPreferences(preferences, &user.NotificationPreferences); err != nil {
			return nil, err
		}
		values = append(values, user)
	}
	return values, rows.Err()
}

func (r *IdentityRepository) CreateUser(ctx context.Context, user domain.AppUser) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into identity.users (id, email, password_hash, display_name, role, access_blocked, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, $7, $8)`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.Role, user.AccessBlocked, user.CreatedAt, user.UpdatedAt)
	return err
}

// UpdateUser replaces profile, authorization, and notification preference fields.
func (r *IdentityRepository) UpdateUser(ctx context.Context, user domain.AppUser) error {
	preferences, err := json.Marshal(user.NotificationPreferences)
	if err != nil {
		return fmt.Errorf("marshal notification preferences: %w", err)
	}
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.users set email=$2,password_hash=$3,display_name=$4,profile_image_object_key=nullif($5,''),role=$6,access_blocked=$7,email_notifications_enabled=$8,notification_preferences=$9,ai_enabled=$10,updated_at=$11 where id=$1 and deleted_at is null`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.ProfileImageObjectKey, user.Role, user.AccessBlocked, user.EmailNotificationsEnabled, preferences, user.AIEnabled, user.UpdatedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// unmarshalPreferences decodes a jsonb notification_preferences column into
// preferences, treating an empty column (never written, or written as '{}')
// as no overrides rather than an error.
func unmarshalPreferences(raw []byte, preferences *map[domain.NotificationKind]bool) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, preferences)
}

// DeleteUser soft-deletes a user projection.
func (r *IdentityRepository) DeleteUser(ctx context.Context, userID string) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.users set deleted_at=now(),updated_at=now() where id=$1 and deleted_at is null`, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *IdentityRepository) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (domain.Invitation, error) {
	var invitation domain.Invitation
	var role, status string
	var acceptedAt *time.Time
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select id::text, email, role::text, token_hash, status::text, invited_by::text, expires_at, created_at, coalesce(accepted_by::text, ''), accepted_at from identity.invitations where token_hash = $1`, hashBytes(tokenHash)).Scan(&invitation.ID, &invitation.Email, &role, &invitation.TokenHash, &status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.CreatedAt, &invitation.AcceptedBy, &acceptedAt)
	invitation.Role, invitation.Status, invitation.AcceptedAt = domain.UserRole(role), domain.InvitationStatus(status), acceptedAt
	return invitation, err
}

func (r *IdentityRepository) CreateInvitation(ctx context.Context, invitation domain.Invitation) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into identity.invitations (id, email, role, token_hash, status, invited_by, expires_at, created_at) values ($1, $2, $3, $4, $5, $6, $7, $8)`, invitation.ID, invitation.Email, invitation.Role, hashBytes(invitation.TokenHash), invitation.Status, invitation.InvitedBy, invitation.ExpiresAt, invitation.CreatedAt)
	return err
}

func (r *IdentityRepository) AcceptInvitation(ctx context.Context, invitationID string, user domain.AppUser) error {
	return pgxtx.Atomic(ctx, r.pool, func(ctx context.Context, querier pgxtx.Querier) error {
		command, err := querier.Exec(ctx, `update identity.invitations set status = 'accepted', accepted_by = $2, accepted_at = now() where id = $1 and status = 'pending' and expires_at > now()`, invitationID, user.ID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return pgx.ErrNoRows
		}
		_, err = querier.Exec(ctx, `insert into identity.users (id, email, password_hash, display_name, role, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, $7)`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.Role, user.CreatedAt, user.UpdatedAt)
		return err
	})
}

func (r *IdentityRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	var token domain.RefreshToken
	var hash []byte
	var revokedAt *time.Time
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select id::text, user_id::text, token_hash, family_id::text, expires_at, created_at, revoked_at, coalesce(replaced_by::text, '') from identity.refresh_tokens where token_hash = $1`, hashBytes(tokenHash)).Scan(&token.ID, &token.UserID, &hash, &token.FamilyID, &token.ExpiresAt, &token.CreatedAt, &revokedAt, &token.ReplacedBy)
	token.TokenHash, token.RevokedAt = hex.EncodeToString(hash), revokedAt
	return token, err
}

func (r *IdentityRepository) CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into identity.refresh_tokens (id, user_id, token_hash, family_id, expires_at, created_at) values ($1, $2, $3, $4, $5, $6)`, token.ID, token.UserID, hashBytes(token.TokenHash), token.FamilyID, token.ExpiresAt, token.CreatedAt)
	return err
}

func (r *IdentityRepository) RotateRefreshToken(ctx context.Context, oldTokenID string, replacement domain.RefreshToken) error {
	return pgxtx.Atomic(ctx, r.pool, func(ctx context.Context, querier pgxtx.Querier) error {
		var familyID string
		if err := querier.QueryRow(ctx, `update identity.refresh_tokens set revoked_at = now(), replaced_by = $2 where id = $1 and revoked_at is null returning family_id::text`, oldTokenID, replacement.ID).Scan(&familyID); err != nil {
			return err
		}
		_, err := querier.Exec(ctx, `insert into identity.refresh_tokens (id, user_id, token_hash, family_id, expires_at, created_at) values ($1, $2, $3, $4, $5, $6)`, replacement.ID, replacement.UserID, hashBytes(replacement.TokenHash), familyID, replacement.ExpiresAt, replacement.CreatedAt)
		return err
	})
}

func (r *IdentityRepository) RevokeRefreshToken(ctx context.Context, tokenID string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.refresh_tokens set revoked_at = now() where id = $1 and revoked_at is null`, tokenID)
	return err
}

// RevokeAllRefreshTokens revokes every live refresh token for userID.
func (r *IdentityRepository) RevokeAllRefreshTokens(ctx context.Context, userID string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.refresh_tokens set revoked_at = now() where user_id = $1 and revoked_at is null`, userID)
	return err
}

func (r *IdentityRepository) GetPasswordResetByTokenHash(ctx context.Context, tokenHash string) (domain.PasswordReset, error) {
	var reset domain.PasswordReset
	var usedAt *time.Time
	err := pgxtx.From(ctx, r.pool).QueryRow(ctx, `select id::text, user_id::text, expires_at, created_at, used_at from identity.password_resets where token_hash = $1`, hashBytes(tokenHash)).Scan(&reset.ID, &reset.UserID, &reset.ExpiresAt, &reset.CreatedAt, &usedAt)
	reset.TokenHash, reset.UsedAt = tokenHash, usedAt
	return reset, err
}

func (r *IdentityRepository) CreatePasswordReset(ctx context.Context, reset domain.PasswordReset) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into identity.password_resets (id, user_id, token_hash, expires_at, created_at) values ($1, $2, $3, $4, $5)`, reset.ID, reset.UserID, hashBytes(reset.TokenHash), reset.ExpiresAt, reset.CreatedAt)
	return err
}

func (r *IdentityRepository) MarkPasswordResetUsed(ctx context.Context, resetID string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.password_resets set used_at = now() where id = $1 and used_at is null`, resetID)
	return err
}

// InvalidatePendingPasswordResets marks every unused reset for userID as used.
func (r *IdentityRepository) InvalidatePendingPasswordResets(ctx context.Context, userID string) error {
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update identity.password_resets set used_at = now() where user_id = $1 and used_at is null`, userID)
	return err
}

func hashBytes(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(fmt.Sprintf("invalid token hash: %v", err))
	}
	return decoded
}
