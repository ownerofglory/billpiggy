package cached_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/cached"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestCachedGroupRepositoryClearsOnCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := memory.NewGroupRepository()
	repository := cached.NewGroupRepository(inner, time.Minute)

	initial, err := repository.ListVisibleGroups(ctx, "owner-1", false)
	if err != nil || len(initial) != 0 {
		t.Fatalf("initial groups = %#v, %v", initial, err)
	}
	if err := repository.CreateGroup(ctx, domain.UserGroup{ID: "group-1", CreatedBy: "owner-1", MemberIDs: []string{"owner-1"}}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	after, err := repository.ListVisibleGroups(ctx, "owner-1", false)
	if err != nil || len(after) != 1 {
		t.Fatalf("groups after create = %#v, %v, want the new group visible immediately", after, err)
	}
}

func TestCachedGroupRepositoryScopesByViewerAndSuperAdminFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := memory.NewGroupRepository()
	if err := inner.CreateGroup(ctx, domain.UserGroup{ID: "group-1", CreatedBy: "owner-1", MemberIDs: []string{"owner-1"}}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	repository := cached.NewGroupRepository(inner, time.Minute)

	memberView, err := repository.ListVisibleGroups(ctx, "owner-1", false)
	if err != nil || len(memberView) != 1 {
		t.Fatalf("member view = %#v, %v", memberView, err)
	}
	outsiderView, err := repository.ListVisibleGroups(ctx, "someone-else", false)
	if err != nil || len(outsiderView) != 0 {
		t.Fatalf("outsider view = %#v, %v, want none visible", outsiderView, err)
	}
	superAdminView, err := repository.ListVisibleGroups(ctx, "someone-else", true)
	if err != nil || len(superAdminView) != 1 {
		t.Fatalf("super admin view = %#v, %v, want every group visible", superAdminView, err)
	}
}
