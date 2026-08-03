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
}

// AuthService implements password login, refresh rotation, invitation acceptance, and RBAC.
type AuthService struct {
	repository outbound.IdentityRepository
	config     AuthConfig
	now        func() time.Time
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

	return &AuthService{repository: repository, config: config, now: time.Now}, nil
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
	return InvitationDelivery{Invitation: invitation, RawToken: rawToken}, nil
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
	return domain.AppUser{ID: uuid.NewString(), Email: normalizeEmail(email), PasswordHash: string(hash), DisplayName: strings.TrimSpace(displayName), Role: role, EmailNotificationsEnabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

// ListUsers returns active users for an administrator.
func (s *AuthService) ListUsers(ctx context.Context, actor domain.AppUser) ([]domain.AppUser, error) {
	if !actor.Role.Allows(domain.PermissionUsersManage) {
		return nil, ErrForbidden
	}
	return s.repository.ListUsers(ctx)
}

// UpdateProfile changes a user's own profile and notification preference.
func (s *AuthService) UpdateProfile(ctx context.Context, userID, displayName, email string, notifications bool) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
	}
	user.DisplayName, user.Email, user.EmailNotificationsEnabled, user.UpdatedAt = strings.TrimSpace(displayName), normalizeEmail(email), notifications, s.now()
	if user.DisplayName == "" || user.Email == "" {
		return domain.AppUser{}, ErrConflict
	}
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	return user, nil
}

// GetProfile returns the current user's profile projection.
func (s *AuthService) GetProfile(ctx context.Context, userID string) (domain.AppUser, error) {
	user, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		return domain.AppUser{}, ErrNotFound
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
	user.Role, user.AccessBlocked, user.UpdatedAt = role, blocked, s.now()
	if err := s.repository.UpdateUser(ctx, user); err != nil {
		return domain.AppUser{}, err
	}
	return user, nil
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
	return s.repository.DeleteUser(ctx, userID)
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
