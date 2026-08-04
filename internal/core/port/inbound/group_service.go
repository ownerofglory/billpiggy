package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// GroupService is everything an HTTP handler needs from group commands and queries.
type GroupService interface {
	CreateGroup(ctx context.Context, actor domain.AppUser, name string, members []string) (domain.UserGroup, error)
	ListVisibleGroups(ctx context.Context, viewer domain.AppUser) ([]domain.UserGroup, error)
	UpdateGroup(ctx context.Context, actor domain.AppUser, groupID, name string) (domain.UserGroup, error)
	DeleteGroup(ctx context.Context, actor domain.AppUser, groupID string) error
	AddMember(ctx context.Context, actor domain.AppUser, groupID, userID string) error
	RemoveMember(ctx context.Context, actor domain.AppUser, groupID, userID string) error
}
