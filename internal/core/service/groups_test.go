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
