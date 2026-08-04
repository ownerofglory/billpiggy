// Package service contains application services that coordinate domain policies and ports.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

var (
	// ErrUnauthorized must not reveal whether a user, password, or refresh token exists.
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrBootstrap    = errors.New("super-admin bootstrap credentials are required")
)

// AuthConfig controls token expiry and required bootstrap credentials.
type AuthConfig struct {
	JWTSecret                   string
	Issuer                      string
	AccessTokenTTL              time.Duration
	RefreshTokenTTL             time.Duration
	InvitationTTL               time.Duration
	BootstrapSuperAdminEmail    string
	BootstrapSuperAdminPassword string
	// PublicBaseURL, when set, lets Invite build a clickable accept-invitation
	// link. Without it, the invitation email carries the raw code instead.
	PublicBaseURL string
}

// ObjectResourceProfileImage identifies profile images to the object
// reference tracker.
const ObjectResourceProfileImage = "user_profile_image"

// AuthService implements password login, refresh rotation, invitation acceptance, and RBAC.
type AuthService struct {
	repository    outbound.IdentityRepository
	config        AuthConfig
	objectRefs    outbound.ObjectReferenceRepository
	notifications outbound.NotificationRepository
	now           func() time.Time
	ids           func() string
}

// NewAuthService creates an auth service. JWT secrets shorter than 32 bytes are rejected.
func NewAuthService(repository outbound.IdentityRepository, config AuthConfig) (*AuthService, error) {
	if repository == nil {
		return nil, errors.New("identity repository is required")
	}
	if len(config.JWTSecret) < 32 {
		return nil, errors.New("JWT secret must contain at least 32 bytes")
	}
	if config.Issuer == "" {
		config.Issuer = "billpiggy"
	}
	if config.AccessTokenTTL <= 0 {
		config.AccessTokenTTL = 15 * time.Minute
	}
	if config.RefreshTokenTTL <= 0 {
		config.RefreshTokenTTL = 30 * 24 * time.Hour
	}
	if config.InvitationTTL <= 0 {
		config.InvitationTTL = 7 * 24 * time.Hour
	}

	return &AuthService{repository: repository, config: config, now: time.Now, ids: uuid.NewString}, nil
}

// WithNotifications enables the invitation and access-changed email
// producers. Without it, Invite and ManageUser skip queuing and only the
// raw invitation token (returned to the caller) or the updated user record
// (returned from ManageUser) reflects the change; no email is ever queued.
func (s *AuthService) WithNotifications(notifications outbound.NotificationRepository) *AuthService {
	s.notifications = notifications
	return s
}

// WithObjectReferences enables profile-image retention tracking. Without it,
// UpdateProfileImage and DeleteUser skip tracking and replaced or orphaned
// profile images are never reclaimed.
func (s *AuthService) WithObjectReferences(references outbound.ObjectReferenceRepository) *AuthService {
	s.objectRefs = references
	return s
}

// EnsureBootstrapSuperAdmin creates the first protected administrator when the database is empty.
func (s *AuthService) EnsureBootstrapSuperAdmin(ctx context.Context) error {
	count, err := s.repository.CountSuperAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count super admins: %w", err)
	}
	if count > 0 {
		return nil
	}
	if s.config.BootstrapSuperAdminEmail == "" || s.config.BootstrapSuperAdminPassword == "" {
		return ErrBootstrap
	}

	user, err := newUser(s.config.BootstrapSuperAdminEmail, s.config.BootstrapSuperAdminPassword, "Super admin", domain.RoleSuperAdmin, s.now())
	if err != nil {
		return err
	}
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("create bootstrap super admin: %w", err)
	}

	return nil
}

// Login verifies credentials and issues a short-lived access token plus a rotating refresh token.
func (s *AuthService) Login(ctx context.Context, email, password string) (Session, error) {
	user, err := s.repository.GetUserByEmail(ctx, normalizeEmail(email))
	if err != nil || user.AccessBlocked || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return Session{}, ErrUnauthorized
	}

	return s.issueSession(ctx, user, "")
}

// Refresh rotates a refresh token. A replayed token cannot be used twice.
func (s *AuthService) Refresh(ctx context.Context, rawToken string) (Session, error) {
	token, err := s.repository.GetRefreshTokenByHash(ctx, tokenHash(rawToken))
	if err != nil || token.RevokedAt != nil || !token.ExpiresAt.After(s.now()) {
		return Session{}, ErrUnauthorized
	}
	user, err := s.repository.GetUserByID(ctx, token.UserID)
	if err != nil || user.AccessBlocked {
		return Session{}, ErrUnauthorized
	}

	return s.issueSession(ctx, user, token.ID)
}

// Logout revokes the supplied refresh token. The corresponding access token naturally expires.
func (s *AuthService) Logout(ctx context.Context, rawToken string) error {
	token, err := s.repository.GetRefreshTokenByHash(ctx, tokenHash(rawToken))
	if err != nil {
		return nil
	}
	return s.repository.RevokeRefreshToken(ctx, token.ID)
}

// Invite creates an invitation. The raw token is returned only for delivery by an email adapter.
func (s *AuthService) Invite(ctx context.Context, actor domain.AppUser, email string, role domain.UserRole) (InvitationDelivery, error) {
	if !actor.Role.Allows(domain.PermissionUsersInvite) || role == domain.RoleSuperAdmin {
		return InvitationDelivery{}, ErrForbidden
	}
	if !validRole(role) {
		return InvitationDelivery{}, ErrConflict
	}
	rawToken, err := randomToken()
	if err != nil {
		return InvitationDelivery{}, err
	}
	now := s.now()
	invitation := domain.Invitation{
		ID: uuid.NewString(), Email: normalizeEmail(email), Role: role, TokenHash: tokenHash(rawToken),
		Status: domain.InvitationPending, InvitedBy: actor.ID, ExpiresAt: now.Add(s.config.InvitationTTL), CreatedAt: now,
	}
	if invitation.Email == "" {
		return InvitationDelivery{}, ErrConflict
	}
	if err := s.repository.CreateInvitation(ctx, invitation); err != nil {
		return InvitationDelivery{}, fmt.Errorf("create invitation: %w", err)
	}
	if err := s.queueInvitationEmail(ctx, invitation, rawToken); err != nil {
		return InvitationDelivery{}, fmt.Errorf("queue invitation email: %w", err)
	}
	return InvitationDelivery{Invitation: invitation, RawToken: rawToken}, nil
}

// queueInvitationEmail queues the invitation notification. Its payload
// necessarily carries the raw token — the invitation row only ever stores a
// one-way hash of it, so this is the only point the plaintext exists to be
// emailed at all. See [domain.NotificationDelivery.Payload] for how that
// exposure is bounded.
func (s *AuthService) queueInvitationEmail(ctx context.Context, invitation domain.Invitation, rawToken string) error {
	if s.notifications == nil {
		return nil
	}
	payload := map[string]string{
		"role":       string(invitation.Role),
		"token":      rawToken,
		"expires_at": invitation.ExpiresAt.Format(time.RFC3339),
	}
	if s.config.PublicBaseURL != "" {
		payload["accept_url"] = strings.TrimRight(s.config.PublicBaseURL, "/") + "/accept-invitation?token=" + rawToken
	}
	return s.notifications.QueueNotification(ctx, domain.NotificationDelivery{
		ID: s.ids(), RecipientEmail: invitation.Email, Kind: domain.NotificationInvitation, Payload: payload, CreatedAt: s.now(), Status: domain.NotificationPending,
	})
}

// AcceptInvitation creates a user from a valid invitation token. Self-registration is not supported.
func (s *AuthService) AcceptInvitation(ctx context.Context, rawToken, password, displayName string) (domain.AppUser, error) {
	invitation, err := s.repository.GetInvitationByTokenHash(ctx, tokenHash(rawToken))
	if err != nil || invitation.Status != domain.InvitationPending || !invitation.ExpiresAt.After(s.now()) {
		return domain.AppUser{}, ErrUnauthorized
	}
	user, err := newUser(invitation.Email, password, displayName, invitation.Role, s.now())
	if err != nil {
		return domain.AppUser{}, err
	}
	if err := s.repository.AcceptInvitation(ctx, invitation.ID, user); err != nil {
		return domain.AppUser{}, fmt.Errorf("accept invitation: %w", err)
	}
	return user, nil
}

// AuthenticateAccessToken returns the current user, rather than trusting stale role claims.
func (s *AuthService) AuthenticateAccessToken(ctx context.Context, rawToken string) (domain.AppUser, error) {
	claims := &accessClaims{}
	_, err := jwt.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, ErrUnauthorized
		}
		return []byte(s.config.JWTSecret), nil
	}, jwt.WithIssuer(s.config.Issuer))
	if err != nil || claims.Subject == "" {
		return domain.AppUser{}, ErrUnauthorized
	}
	user, err := s.repository.GetUserByID(ctx, claims.Subject)
	if err != nil || user.AccessBlocked {
		return domain.AppUser{}, ErrUnauthorized
	}
	return user, nil
}

// Session contains the values an HTTP adapter must deliver to the frontend.
type Session struct {
	AccessToken        string
	AccessTokenExpiry  time.Time
	RefreshToken       string
	RefreshTokenExpiry time.Time
}

// InvitationDelivery is intentionally consumed by a notification adapter, not returned to users.
type InvitationDelivery struct {
	Invitation domain.Invitation
	RawToken   string
}

type accessClaims struct {
	Role domain.UserRole `json:"role"`
	jwt.RegisteredClaims
}

func (s *AuthService) issueSession(ctx context.Context, user domain.AppUser, replacedTokenID string) (Session, error) {
	now := s.now()
	accessExpiry := now.Add(s.config.AccessTokenTTL)
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{
		Role:             user.Role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID, Issuer: s.config.Issuer, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(accessExpiry)},
	}).SignedString([]byte(s.config.JWTSecret))
	if err != nil {
		return Session{}, fmt.Errorf("sign access token: %w", err)
	}

	rawRefreshToken, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	refreshExpiry := now.Add(s.config.RefreshTokenTTL)
	refreshToken := domain.RefreshToken{ID: uuid.NewString(), UserID: user.ID, TokenHash: tokenHash(rawRefreshToken), FamilyID: uuid.NewString(), ExpiresAt: refreshExpiry, CreatedAt: now}
	if replacedTokenID == "" {
		err = s.repository.CreateRefreshToken(ctx, refreshToken)
	} else {
		err = s.repository.RotateRefreshToken(ctx, replacedTokenID, refreshToken)
	}
	if err != nil {
		return Session{}, fmt.Errorf("store refresh token: %w", err)
	}

	return Session{AccessToken: accessToken, AccessTokenExpiry: accessExpiry, RefreshToken: rawRefreshToken, RefreshTokenExpiry: refreshExpiry}, nil
}

func newUser(email, password, displayName string, role domain.UserRole, now time.Time) (domain.AppUser, error) {
	if normalizeEmail(email) == "" || strings.TrimSpace(displayName) == "" || len(password) < 12 || !validRole(role) {
		return domain.AppUser{}, ErrConflict
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.AppUser{}, fmt.Errorf("hash password: %w", err)
	}
	return domain.AppUser{ID: uuid.NewString(), Email: normalizeEmail(email), PasswordHash: string(hash), DisplayName: strings.TrimSpace(displayName), Role: role, EmailNotificationsEnabled: true, AIEnabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

// ListUsers returns active users for an administrator.
func (s *AuthService) ListUsers(ctx context.Context, actor domain.AppUser) ([]domain.AppUser, error) {
	if !actor.Role.Allows(domain.PermissionUsersManage) {
		return nil, ErrForbidden
	}
	return s.repository.ListUsers(ctx)
}

// UpdateProfile changes a user's own profile, notification preference, and AI
// opt-in setting.
func (s *AuthService) UpdateProfile(ctx context.Context, userID, displayName, email string, notifications, aiEnabled bool) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	user.DisplayName, user.Email, user.EmailNotificationsEnabled, user.AIEnabled, user.UpdatedAt = strings.TrimSpace(displayName), normalizeEmail(email), notifications, aiEnabled, s.now()
	if user.DisplayName == "" || user.Email == "" {
		return domain.AppUser{}, ErrConflict
	}
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	return user, nil
}

// UpdateNotificationPreferences replaces the current user's per-kind
// overrides of EmailNotificationsEnabled. A kind absent from preferences
// falls back to that master switch; see [domain.AppUser.WantsNotification].
func (s *AuthService) UpdateNotificationPreferences(ctx context.Context, userID string, preferences map[domain.NotificationKind]bool) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	user.NotificationPreferences, user.UpdatedAt = preferences, s.now()
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	return user, nil
}

// ChangePassword verifies currentPassword, rotates the stored hash to
// newPassword, and revokes every live refresh token for the user, so a
// session established under the old password (e.g. on a device that stole a
// refresh token) cannot silently keep renewing access after the change.
func (s *AuthService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return ErrNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)) != nil {
		return ErrUnauthorized
	}
	if len(newPassword) < 12 {
		return ErrConflict
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user.PasswordHash, user.UpdatedAt = string(hash), s.now()
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return err
	}
	return s.repository.RevokeAllRefreshTokens(ctx, userID)
}

// GetProfile returns the current user's profile projection.
func (s *AuthService) GetProfile(ctx context.Context, userID string) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	return user, nil
}

// UpdateProfileImage records the object key for the current user's profile
// image. A previously stored image is orphaned so the retention sweeper
// reclaims it.
func (s *AuthService) UpdateProfileImage(ctx context.Context, userID, objectKey string) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	if strings.TrimSpace(objectKey) == "" {
		return domain.AppUser{}, ErrConflict
	}
	previousKey := user.ProfileImageObjectKey
	user.ProfileImageObjectKey, user.UpdatedAt = objectKey, s.now()
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	if s.objectRefs != nil {
		if err := s.objectRefs.TrackObject(ctx, domain.ObjectReference{
			ObjectKey: objectKey, OwnerID: userID, ResourceType: ObjectResourceProfileImage, ResourceID: userID,
		}); err != nil {
			return domain.AppUser{}, fmt.Errorf("track profile image object: %w", err)
		}
		if previousKey != "" && previousKey != objectKey {
			if err := s.objectRefs.OrphanObjectsFor(ctx, ObjectResourceProfileImage, userID, objectKey); err != nil {
				return domain.AppUser{}, fmt.Errorf("orphan previous profile image: %w", err)
			}
		}
	}
	return user, nil
}

// ManageUser changes a member/admin role or access state while preserving super-admin protections.
func (s *AuthService) ManageUser(ctx context.Context, actor domain.AppUser, userID string, role domain.UserRole, blocked bool) (domain.AppUser, error) {
	if !actor.Role.Allows(domain.PermissionUsersManage) {
		return domain.AppUser{}, ErrForbidden
	}
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	if user.Role == domain.RoleSuperAdmin || role == domain.RoleSuperAdmin {
		return domain.AppUser{}, ErrForbidden
	}
	if !validRole(role) || role == domain.RoleSuperAdmin {
		return domain.AppUser{}, ErrConflict
	}
	changed := user.Role != role || user.AccessBlocked != blocked
	user.Role, user.AccessBlocked, user.UpdatedAt = role, blocked, s.now()
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	if changed {
		if err := s.queueAccessChangedEmail(ctx, user); err != nil {
			return domain.AppUser{}, fmt.Errorf("queue access changed email: %w", err)
		}
	}
	return user, nil
}

// queueAccessChangedEmail queues the access-changed notification. Unlike
// most notification producers this can't sit behind the user's own
// preference toggle: the whole point is telling someone their access just
// changed, possibly including being blocked, so honouring an opt-out here
// would let a blocked user silently miss the one email that explains why.
func (s *AuthService) queueAccessChangedEmail(ctx context.Context, user domain.AppUser) error {
	if s.notifications == nil {
		return nil
	}
	payload := map[string]string{
		"role":    string(user.Role),
		"blocked": strconv.FormatBool(user.AccessBlocked),
	}
	return s.notifications.QueueNotification(ctx, domain.NotificationDelivery{
		ID: s.ids(), RecipientEmail: user.Email, Kind: domain.NotificationAccessChanged, Payload: payload, CreatedAt: s.now(), Status: domain.NotificationPending,
	})
}

// DeleteUser removes a member or administrator but never a super-admin.
func (s *AuthService) DeleteUser(ctx context.Context, actor domain.AppUser, userID string) error {
	if !actor.Role.Allows(domain.PermissionUsersManage) {
		return ErrForbidden
	}
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return ErrNotFound
	}
	if user.Role == domain.RoleSuperAdmin {
		return ErrForbidden
	}
	if err := s.repository.DeleteUser(ctx, userID); err != nil {
		return err
	}
	if s.objectRefs != nil {
		if err := s.objectRefs.OrphanObjectsFor(ctx, ObjectResourceProfileImage, userID, ""); err != nil {
			return fmt.Errorf("orphan profile image for deleted user: %w", err)
		}
	}
	return nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func tokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validRole(role domain.UserRole) bool {
	return role == domain.RoleMember || role == domain.RoleAdmin || role == domain.RoleSuperAdmin
}
