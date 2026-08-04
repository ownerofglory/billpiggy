package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// GroupRepository is an in-memory private-group projection.
type GroupRepository struct {
	mu     sync.RWMutex
	groups map[string]domain.UserGroup
}

// NewGroupRepository creates an empty group projection.
func NewGroupRepository() *GroupRepository {
	return &GroupRepository{groups: map[string]domain.UserGroup{}}
}

// CreateGroup stores a private group in the projection.
func (r *GroupRepository) CreateGroup(_ context.Context, g domain.UserGroup) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups[g.ID] = g
	return nil
}

// ListVisibleGroups returns groups available to the viewer.
func (r *GroupRepository) ListVisibleGroups(_ context.Context, viewer string, super bool) ([]domain.UserGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []domain.UserGroup{}
	for _, g := range r.groups {
		if super || g.CreatedBy == viewer || hasMember(g.MemberIDs, viewer) {
			out = append(out, g)
		}
	}
	return out, nil
}

// GetGroup returns one group regardless of visibility.
func (r *GroupRepository) GetGroup(_ context.Context, groupID string) (domain.UserGroup, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.groups[groupID]
	if !ok {
		return domain.UserGroup{}, errNotFound
	}
	return g, nil
}

// UpdateGroup renames a group.
func (r *GroupRepository) UpdateGroup(_ context.Context, groupID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok {
		return errNotFound
	}
	g.Name = name
	r.groups[groupID] = g
	return nil
}

// DeleteGroup removes a group and its memberships.
func (r *GroupRepository) DeleteGroup(_ context.Context, groupID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.groups[groupID]; !ok {
		return errNotFound
	}
	delete(r.groups, groupID)
	return nil
}

// AddMember adds a member, or does nothing if already a member.
func (r *GroupRepository) AddMember(_ context.Context, groupID, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok {
		return errNotFound
	}
	if hasMember(g.MemberIDs, userID) {
		return nil
	}
	g.MemberIDs = append(append([]string(nil), g.MemberIDs...), userID)
	r.groups[groupID] = g
	return nil
}

// RemoveMember removes a member, or does nothing if not a member.
func (r *GroupRepository) RemoveMember(_ context.Context, groupID, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok {
		return errNotFound
	}
	members := make([]string, 0, len(g.MemberIDs))
	for _, id := range g.MemberIDs {
		if id != userID {
			members = append(members, id)
		}
	}
	g.MemberIDs = members
	r.groups[groupID] = g
	return nil
}
func hasMember(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}
