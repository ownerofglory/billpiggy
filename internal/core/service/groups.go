package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"strings"
	"time"
)

// GroupService applies administrator ownership and super-admin visibility rules.
type GroupService struct {
	repository outbound.GroupRepository
	now        func() time.Time
}

// NewGroupService creates a group service.
func NewGroupService(repository outbound.GroupRepository) (*GroupService, error) {
	if repository == nil {
		return nil, errors.New("group repository is required")
	}
	return &GroupService{repository: repository, now: time.Now}, nil
}

// CreateGroup creates a private group owned by the administrator actor.
func (s *GroupService) CreateGroup(ctx context.Context, actor domain.AppUser, name string, members []string) (domain.UserGroup, error) {
	if actor.Role != domain.RoleAdmin && actor.Role != domain.RoleSuperAdmin {
		return domain.UserGroup{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.UserGroup{}, errors.New("group name is required")
	}
	value := domain.UserGroup{ID: uuid.NewString(), Name: name, CreatedBy: actor.ID, CreatedAt: s.now(), MemberIDs: append([]string(nil), members...)}
	return value, s.repository.CreateGroup(ctx, value)
}

// ListVisibleGroups returns groups owned by or shared with a user, or all groups for a super-admin.
func (s *GroupService) ListVisibleGroups(ctx context.Context, viewer domain.AppUser) ([]domain.UserGroup, error) {
	return s.repository.ListVisibleGroups(ctx, viewer.ID, viewer.Role == domain.RoleSuperAdmin)
}
