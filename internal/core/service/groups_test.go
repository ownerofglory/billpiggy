package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestGroupServiceScopesVisibilityAndCreation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	groups, err := service.NewGroupService(memory.NewGroupRepository())
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.AppUser{ID: "admin", Role: domain.RoleAdmin}
	created, err := groups.CreateGroup(ctx, admin, " Household ", []string{"member"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if created.Name != "Household" {
		t.Fatalf("group name = %q", created.Name)
	}
	memberGroups, err := groups.ListVisibleGroups(ctx, domain.AppUser{ID: "member", Role: domain.RoleMember})
	if err != nil || len(memberGroups) != 1 || memberGroups[0].ID != created.ID {
		t.Fatalf("member visibility = %#v, %v", memberGroups, err)
	}
	if values, err := groups.ListVisibleGroups(ctx, domain.AppUser{ID: "other", Role: domain.RoleMember}); err != nil || len(values) != 0 {
		t.Fatalf("unrelated visibility = %#v, %v", values, err)
	}
	if _, err := groups.CreateGroup(ctx, domain.AppUser{ID: "member", Role: domain.RoleMember}, "No", nil); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("member create error = %v", err)
	}
}

func TestGroupServiceUpdateDeleteAndMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	groups, err := service.NewGroupService(memory.NewGroupRepository())
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.AppUser{ID: "admin-1", Role: domain.RoleAdmin}
	created, err := groups.CreateGroup(ctx, admin, "Household", []string{"member-1"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	// The creator can rename it.
	updated, err := groups.UpdateGroup(ctx, admin, created.ID, " Roommates ")
	if err != nil || updated.Name != "Roommates" {
		t.Fatalf("update group: %#v, %v", updated, err)
	}

	// A different admin (not the creator, not super-admin) cannot manage it.
	otherAdmin := domain.AppUser{ID: "admin-2", Role: domain.RoleAdmin}
	if _, err := groups.UpdateGroup(ctx, otherAdmin, created.ID, "Hijacked"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("non-owner update: err = %v, want ErrForbidden", err)
	}
	if err := groups.AddMember(ctx, otherAdmin, created.ID, "member-2"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("non-owner add member: err = %v, want ErrForbidden", err)
	}

	// A super-admin can manage any group.
	superAdmin := domain.AppUser{ID: "root", Role: domain.RoleSuperAdmin}
	if err := groups.AddMember(ctx, superAdmin, created.ID, "member-2"); err != nil {
		t.Fatalf("super-admin add member: %v", err)
	}
	visible, err := groups.ListVisibleGroups(ctx, domain.AppUser{ID: "member-2", Role: domain.RoleMember})
	if err != nil || len(visible) != 1 {
		t.Fatalf("member-2 visibility after add = %#v, %v", visible, err)
	}

	if err := groups.RemoveMember(ctx, admin, created.ID, "member-2"); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	visible, err = groups.ListVisibleGroups(ctx, domain.AppUser{ID: "member-2", Role: domain.RoleMember})
	if err != nil || len(visible) != 0 {
		t.Fatalf("member-2 visibility after remove = %#v, %v", visible, err)
	}

	if err := groups.DeleteGroup(ctx, admin, created.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := groups.UpdateGroup(ctx, admin, created.ID, "Ghost"); err != service.ErrNotFound {
		t.Fatalf("update deleted group: err = %v, want ErrNotFound", err)
	}
}

func TestGroupServiceManageUnknownGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	groups, err := service.NewGroupService(memory.NewGroupRepository())
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.AppUser{ID: "admin-1", Role: domain.RoleAdmin}
	if _, err := groups.UpdateGroup(ctx, admin, "does-not-exist", "x"); err != service.ErrNotFound {
		t.Fatalf("update unknown group: err = %v, want ErrNotFound", err)
	}
	if err := groups.DeleteGroup(ctx, admin, "does-not-exist"); err != service.ErrNotFound {
		t.Fatalf("delete unknown group: err = %v, want ErrNotFound", err)
	}
}
