package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// GroupRepository owns private group projections.
type GroupRepository interface {
	CreateGroup(context.Context, domain.UserGroup) error
	ListVisibleGroups(context.Context, string, bool) ([]domain.UserGroup, error)
}
