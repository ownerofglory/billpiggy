package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
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
	// []string{} rather than ([]string)(nil): append onto a nil base still
	// returns nil when members is nil/empty, which would serialize the
	// create response as "memberIDs": null instead of [].
	value := domain.UserGroup{ID: uuid.NewString(), Name: name, CreatedBy: actor.ID, CreatedAt: s.now(), MemberIDs: append([]string{}, members...)}
	return value, s.repository.CreateGroup(ctx, value)
}

// ListVisibleGroups returns groups owned by or shared with a user, or all groups for a super-admin.
func (s *GroupService) ListVisibleGroups(ctx context.Context, viewer domain.AppUser) ([]domain.UserGroup, error) {
	return s.repository.ListVisibleGroups(ctx, viewer.ID, viewer.Role == domain.RoleSuperAdmin)
}

// requireGroupOwner loads a group and confirms actor is its creator or a
// super-admin, the access policy every group mutation shares.
func (s *GroupService) requireGroupOwner(ctx context.Context, actor domain.AppUser, groupID string) (domain.UserGroup, error) {
	group, err := s.repository.GetGroup(ctx, groupID)
	if err != nil {
		return domain.UserGroup{}, ErrNotFound
	}
	if actor.Role != domain.RoleSuperAdmin && group.CreatedBy != actor.ID {
		return domain.UserGroup{}, ErrForbidden
	}
	return group, nil
}

// UpdateGroup renames a group. Restricted to the group's creator or a super-admin.
func (s *GroupService) UpdateGroup(ctx context.Context, actor domain.AppUser, groupID, name string) (domain.UserGroup, error) {
	group, err := s.requireGroupOwner(ctx, actor, groupID)
	if err != nil {
		return domain.UserGroup{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.UserGroup{}, errors.New("group name is required")
	}
	if err := s.repository.UpdateGroup(ctx, groupID, name); err != nil {
		return domain.UserGroup{}, err
	}
	group.Name = name
	return group, nil
}

// DeleteGroup removes a group. Restricted to the group's creator or a
// super-admin. Fails if any expense or budget still shares with it.
func (s *GroupService) DeleteGroup(ctx context.Context, actor domain.AppUser, groupID string) error {
	if _, err := s.requireGroupOwner(ctx, actor, groupID); err != nil {
		return err
	}
	return s.repository.DeleteGroup(ctx, groupID)
}

// AddMember adds a member to a group. Restricted to the group's creator or a
// super-admin.
func (s *GroupService) AddMember(ctx context.Context, actor domain.AppUser, groupID, userID string) error {
	if _, err := s.requireGroupOwner(ctx, actor, groupID); err != nil {
		return err
	}
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	return s.repository.AddMember(ctx, groupID, userID)
}

// RemoveMember removes a member from a group. Restricted to the group's
// creator or a super-admin.
func (s *GroupService) RemoveMember(ctx context.Context, actor domain.AppUser, groupID, userID string) error {
	if _, err := s.requireGroupOwner(ctx, actor, groupID); err != nil {
		return err
	}
	return s.repository.RemoveMember(ctx, groupID, userID)
}
