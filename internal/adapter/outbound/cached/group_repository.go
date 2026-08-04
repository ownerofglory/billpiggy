package cached

import (
	"context"
	"strconv"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/cache"
)

// GroupRepository caches ListVisibleGroups per viewer. Groups are created
// far less often than a viewer's visible-groups list is read (every budget
// and expense list call that needs shared-group scoping reads it), and the
// port has no update/delete, so the only invalidation needed is a full clear
// whenever a new group appears — any viewer's visible set could change.
type GroupRepository struct {
	inner  outbound.GroupRepository
	groups *cache.Cache[string, []domain.UserGroup]
}

// NewGroupRepository wraps inner, caching ListVisibleGroups results for ttl.
func NewGroupRepository(inner outbound.GroupRepository, ttl time.Duration) *GroupRepository {
	return &GroupRepository{inner: inner, groups: cache.New[string, []domain.UserGroup](ttl)}
}

// ListVisibleGroups returns the cached list when present, otherwise loads
// and caches it.
func (r *GroupRepository) ListVisibleGroups(ctx context.Context, viewerID string, isSuperAdmin bool) ([]domain.UserGroup, error) {
	key := viewerID + ":" + strconv.FormatBool(isSuperAdmin)
	if values, ok := r.groups.Get(key); ok {
		return values, nil
	}
	values, err := r.inner.ListVisibleGroups(ctx, viewerID, isSuperAdmin)
	if err != nil {
		return nil, err
	}
	r.groups.Set(key, values)
	return values, nil
}

// CreateGroup writes through and clears every cached list: a new group can
// change any viewer's visible set, not just its creator's.
func (r *GroupRepository) CreateGroup(ctx context.Context, group domain.UserGroup) error {
	if err := r.inner.CreateGroup(ctx, group); err != nil {
		return err
	}
	r.groups.Clear()
	return nil
}

// GetGroup delegates uncached: it is used for one-off access-policy checks,
// not the read-heavy path ListVisibleGroups caches.
func (r *GroupRepository) GetGroup(ctx context.Context, groupID string) (domain.UserGroup, error) {
	return r.inner.GetGroup(ctx, groupID)
}

// UpdateGroup writes through and clears every cached list.
func (r *GroupRepository) UpdateGroup(ctx context.Context, groupID, name string) error {
	if err := r.inner.UpdateGroup(ctx, groupID, name); err != nil {
		return err
	}
	r.groups.Clear()
	return nil
}

// DeleteGroup writes through and clears every cached list.
func (r *GroupRepository) DeleteGroup(ctx context.Context, groupID string) error {
	if err := r.inner.DeleteGroup(ctx, groupID); err != nil {
		return err
	}
	r.groups.Clear()
	return nil
}

// AddMember writes through and clears every cached list: the new member's
// visible set now includes this group.
func (r *GroupRepository) AddMember(ctx context.Context, groupID, userID string) error {
	if err := r.inner.AddMember(ctx, groupID, userID); err != nil {
		return err
	}
	r.groups.Clear()
	return nil
}

// RemoveMember writes through and clears every cached list.
func (r *GroupRepository) RemoveMember(ctx context.Context, groupID, userID string) error {
	if err := r.inner.RemoveMember(ctx, groupID, userID); err != nil {
		return err
	}
	r.groups.Clear()
	return nil
}
