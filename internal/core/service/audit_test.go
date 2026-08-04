package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestAuditServiceListEntriesRestrictedToSuperAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewAuditRepository()
	if err := repository.AppendEntry(ctx, domain.AuditEntry{EventID: "event-1", ActorID: "user-1", Action: "expense_added", ResourceType: "expense", ResourceID: "expense-1", OccurredAt: time.Now()}); err != nil {
		t.Fatalf("append entry: %v", err)
	}
	audit, err := service.NewAuditService(repository)
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}

	superAdmin := domain.AppUser{ID: "admin-1", Role: domain.RoleSuperAdmin}
	entries, err := audit.ListEntries(ctx, superAdmin, outbound.AuditFilter{})
	if err != nil {
		t.Fatalf("list entries as super admin: %v", err)
	}
	if len(entries) != 1 || entries[0].EventID != "event-1" {
		t.Fatalf("entries = %#v", entries)
	}

	admin := domain.AppUser{ID: "admin-2", Role: domain.RoleAdmin}
	if _, err := audit.ListEntries(ctx, admin, outbound.AuditFilter{}); err != service.ErrForbidden {
		t.Fatalf("list entries as admin (not super-admin): err = %v, want ErrForbidden", err)
	}

	member := domain.AppUser{ID: "member-1", Role: domain.RoleMember}
	if _, err := audit.ListEntries(ctx, member, outbound.AuditFilter{}); err != service.ErrForbidden {
		t.Fatalf("list entries as member: err = %v, want ErrForbidden", err)
	}
}

func TestAuditServiceDefaultsAndCapsLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewAuditRepository()
	for i := 0; i < 3; i++ {
		if err := repository.AppendEntry(ctx, domain.AuditEntry{EventID: string(rune('a' + i)), Action: "expense_added", ResourceType: "expense", OccurredAt: time.Now()}); err != nil {
			t.Fatalf("append entry: %v", err)
		}
	}
	audit, err := service.NewAuditService(repository)
	if err != nil {
		t.Fatalf("new audit service: %v", err)
	}
	superAdmin := domain.AppUser{ID: "admin-1", Role: domain.RoleSuperAdmin}
	entries, err := audit.ListEntries(ctx, superAdmin, outbound.AuditFilter{Limit: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (default limit of 50 covers all of them)", len(entries))
	}
}
