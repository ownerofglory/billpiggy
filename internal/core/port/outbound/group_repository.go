package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// GroupRepository owns private group projections.
type GroupRepository interface {
	CreateGroup(context.Context, domain.UserGroup) error
	ListVisibleGroups(context.Context, string, bool) ([]domain.UserGroup, error)
	// GetGroup returns one group regardless of visibility, so the service can
	// apply its own creator/super-admin access policy before acting.
	GetGroup(ctx context.Context, groupID string) (domain.UserGroup, error)
	// UpdateGroup renames a group.
	UpdateGroup(ctx context.Context, groupID, name string) error
	// DeleteGroup removes a group and its memberships.
	DeleteGroup(ctx context.Context, groupID string) error
	// AddMember and RemoveMember change a group's membership. Adding an
	// already-present member or removing an absent one is a no-op, not an
	// error, so a client can't be made to retry a partially-applied request.
	AddMember(ctx context.Context, groupID, userID string) error
	RemoveMember(ctx context.Context, groupID, userID string) error
}
