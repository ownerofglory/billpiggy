package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestAdminUsageServiceSummarize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	identity := memory.NewIdentityRepository()
	aiRequests := memory.NewAIRequestRepository()
	notifications := memory.NewNotificationRepository()
	audit := memory.NewAuditRepository()
	usage, err := service.NewAdminUsageService(identity, aiRequests, notifications, audit)
	if err != nil {
		t.Fatalf("new admin usage service: %v", err)
	}

	now := time.Now()
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "admin-1", Email: "admin@example.com", Role: domain.RoleSuperAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "member-1", Email: "member1@example.com", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "member-2", Email: "member2@example.com", Role: domain.RoleMember, AccessBlocked: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create blocked member: %v", err)
	}
	if err := aiRequests.RecordRequest(ctx, domain.AIRequestRecord{ID: "req-1", Workload: domain.AIWorkloadAssistant, Outcome: domain.AIRequestSuccess, Usage: domain.TokenUsage{InputTokens: 10, OutputTokens: 5}, CreatedAt: now}); err != nil {
		t.Fatalf("record ai request: %v", err)
	}
	if err := notifications.QueueNotification(ctx, domain.NotificationDelivery{ID: "delivery-1", UserID: "member-1", Kind: domain.NotificationBudgetAlert, Status: domain.NotificationPending, CreatedAt: now}); err != nil {
		t.Fatalf("queue notification: %v", err)
	}
	if err := audit.AppendEntry(ctx, domain.AuditEntry{EventID: "event-1", Action: "expense_added", ResourceType: "expense", OccurredAt: now}); err != nil {
		t.Fatalf("append audit entry: %v", err)
	}

	admin := domain.AppUser{ID: "admin-1", Role: domain.RoleSuperAdmin}
	summary, err := usage.Summarize(ctx, admin, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary.TotalUsers != 3 {
		t.Fatalf("total users = %d, want 3", summary.TotalUsers)
	}
	if summary.UsersByRole[domain.RoleMember] != 2 || summary.UsersByRole[domain.RoleSuperAdmin] != 1 {
		t.Fatalf("users by role = %#v", summary.UsersByRole)
	}
	if summary.BlockedUsers != 1 {
		t.Fatalf("blocked users = %d, want 1", summary.BlockedUsers)
	}
	if len(summary.AI.ByWorkload) != 1 || summary.AI.ByWorkload[0].RequestCount != 1 || summary.AI.ByWorkload[0].InputTokens != 10 {
		t.Fatalf("ai usage = %#v", summary.AI)
	}
	if summary.NotificationsByStatus[domain.NotificationPending] != 1 {
		t.Fatalf("notifications by status = %#v", summary.NotificationsByStatus)
	}
	if summary.AuditEntryCount != 1 {
		t.Fatalf("audit entry count = %d, want 1", summary.AuditEntryCount)
	}
}

func TestAdminUsageServiceRejectsNonSuperAdmin(t *testing.T) {
	t.Parallel()
	usage, err := service.NewAdminUsageService(memory.NewIdentityRepository(), memory.NewAIRequestRepository(), memory.NewNotificationRepository(), memory.NewAuditRepository())
	if err != nil {
		t.Fatalf("new admin usage service: %v", err)
	}
	admin := domain.AppUser{ID: "admin-1", Role: domain.RoleAdmin}
	if _, err := usage.Summarize(context.Background(), admin, time.Time{}); err != service.ErrForbidden {
		t.Fatalf("summarize as admin (not super-admin): err = %v, want ErrForbidden", err)
	}
}
