// Package memory provides concurrency-safe adapters for tests and local development.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

var errNotFound = errors.New("not found")

// IdentityRepository is an in-memory implementation of the identity outbound port.
type IdentityRepository struct {
	mu                 sync.RWMutex
	usersByID          map[string]domain.AppUser
	userIDByEmail      map[string]string
	invitationsByID    map[string]domain.Invitation
	invitationIDByHash map[string]string
	refreshByID        map[string]domain.RefreshToken
	refreshIDByHash    map[string]string
}

// NewIdentityRepository creates an empty repository.
func NewIdentityRepository() *IdentityRepository {
	return &IdentityRepository{
		usersByID: make(map[string]domain.AppUser), userIDByEmail: make(map[string]string),
		invitationsByID: make(map[string]domain.Invitation), invitationIDByHash: make(map[string]string),
		refreshByID: make(map[string]domain.RefreshToken), refreshIDByHash: make(map[string]string),
	}
}

func (r *IdentityRepository) CountSuperAdmins(_ context.Context) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, user := range r.usersByID {
		if user.Role == domain.RoleSuperAdmin && !user.AccessBlocked {
			count++
		}
	}
	return count, nil
}

func (r *IdentityRepository) GetUserByID(_ context.Context, id string) (domain.AppUser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, ok := r.usersByID[id]
	if !ok {
		return domain.AppUser{}, errNotFound
	}
	return user, nil
}

func (r *IdentityRepository) GetUserByEmail(_ context.Context, email string) (domain.AppUser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.userIDByEmail[email]
	if !ok {
		return domain.AppUser{}, errNotFound
	}
	return r.usersByID[id], nil
}

// ListUsers returns all active user projections.
func (r *IdentityRepository) ListUsers(_ context.Context) ([]domain.AppUser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.AppUser, 0, len(r.usersByID))
	for _, user := range r.usersByID {
		values = append(values, user)
	}
	return values, nil
}

func (r *IdentityRepository) CreateUser(_ context.Context, user domain.AppUser) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.usersByID[user.ID]; exists {
		return errors.New("user id already exists")
	}
	if _, exists := r.userIDByEmail[user.Email]; exists {
		return errors.New("user email already exists")
	}
	r.usersByID[user.ID] = user
	r.userIDByEmail[user.Email] = user.ID
	return nil
}

// UpdateUser replaces an existing user projection.
func (r *IdentityRepository) UpdateUser(_ context.Context, user domain.AppUser) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, exists := r.usersByID[user.ID]
	if !exists {
		return errNotFound
	}
	if previous.Email != user.Email {
		if existing, exists := r.userIDByEmail[user.Email]; exists && existing != user.ID {
			return errors.New("user email already exists")
		}
		delete(r.userIDByEmail, previous.Email)
		r.userIDByEmail[user.Email] = user.ID
	}
	r.usersByID[user.ID] = user
	return nil
}

// DeleteUser removes an active user projection.
func (r *IdentityRepository) DeleteUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, exists := r.usersByID[userID]
	if !exists {
		return errNotFound
	}
	delete(r.usersByID, userID)
	delete(r.userIDByEmail, user.Email)
	return nil
}

func (r *IdentityRepository) GetInvitationByTokenHash(_ context.Context, tokenHash string) (domain.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.invitationIDByHash[tokenHash]
	if !ok {
		return domain.Invitation{}, errNotFound
	}
	return r.invitationsByID[id], nil
}

func (r *IdentityRepository) CreateInvitation(_ context.Context, invitation domain.Invitation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.invitationIDByHash[invitation.TokenHash]; exists {
		return errors.New("invitation token already exists")
	}
	r.invitationsByID[invitation.ID] = invitation
	r.invitationIDByHash[invitation.TokenHash] = invitation.ID
	return nil
}

func (r *IdentityRepository) AcceptInvitation(_ context.Context, invitationID string, user domain.AppUser) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	invitation, ok := r.invitationsByID[invitationID]
	if !ok || invitation.Status != domain.InvitationPending || !invitation.ExpiresAt.After(time.Now()) {
		return errNotFound
	}
	if _, exists := r.userIDByEmail[user.Email]; exists {
		return errors.New("user email already exists")
	}
	now := time.Now()
	invitation.Status = domain.InvitationAccepted
	invitation.AcceptedBy = user.ID
	invitation.AcceptedAt = &now
	r.invitationsByID[invitationID] = invitation
	r.usersByID[user.ID] = user
	r.userIDByEmail[user.Email] = user.ID
	return nil
}

func (r *IdentityRepository) GetRefreshTokenByHash(_ context.Context, tokenHash string) (domain.RefreshToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.refreshIDByHash[tokenHash]
	if !ok {
		return domain.RefreshToken{}, errNotFound
	}
	return r.refreshByID[id], nil
}

func (r *IdentityRepository) CreateRefreshToken(_ context.Context, token domain.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.refreshIDByHash[token.TokenHash]; exists {
		return errors.New("refresh token already exists")
	}
	r.refreshByID[token.ID] = token
	r.refreshIDByHash[token.TokenHash] = token.ID
	return nil
}

func (r *IdentityRepository) RotateRefreshToken(_ context.Context, oldTokenID string, replacement domain.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	oldToken, ok := r.refreshByID[oldTokenID]
	if !ok || oldToken.RevokedAt != nil {
		return errNotFound
	}
	if _, exists := r.refreshIDByHash[replacement.TokenHash]; exists {
		return errors.New("refresh token already exists")
	}
	now := time.Now()
	oldToken.RevokedAt = &now
	oldToken.ReplacedBy = replacement.ID
	replacement.FamilyID = oldToken.FamilyID
	r.refreshByID[oldTokenID] = oldToken
	r.refreshByID[replacement.ID] = replacement
	r.refreshIDByHash[replacement.TokenHash] = replacement.ID
	return nil
}

func (r *IdentityRepository) RevokeRefreshToken(_ context.Context, tokenID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.refreshByID[tokenID]
	if !ok || token.RevokedAt != nil {
		return nil
	}
	now := time.Now()
	token.RevokedAt = &now
	r.refreshByID[tokenID] = token
	return nil
}

// RevokeAllRefreshTokens revokes every live refresh token for userID.
func (r *IdentityRepository) RevokeAllRefreshTokens(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, token := range r.refreshByID {
		if token.UserID == userID && token.RevokedAt == nil {
			token.RevokedAt = &now
			r.refreshByID[id] = token
		}
	}
	return nil
}
