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
func hasMember(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}
