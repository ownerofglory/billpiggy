package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func newTestReportService(t *testing.T) (*service.ReportService, *memory.ExpenseRepository, *memory.IdentityRepository, *memory.ObjectStore, *memory.NotificationRepository) {
	t.Helper()
	reports := memory.NewReportRepository()
	expenses := memory.NewExpenseRepository()
	taxonomy := memory.NewTaxonomyRepository()
	identity := memory.NewIdentityRepository()
	objects := memory.NewObjectStore()
	notifications := memory.NewNotificationRepository()
	svc, err := service.NewReportService(reports, expenses, taxonomy, identity, objects, notifications)
	if err != nil {
		t.Fatalf("new report service: %v", err)
	}
	return svc, expenses, identity, objects, notifications
}

func TestReportServiceGenerateDueCreatesCSVAndPDFOncePerPeriod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, expenses, identity, objects, notifications := newTestReportService(t)

	if err := identity.CreateUser(ctx, domain.AppUser{ID: "owner-1", Email: "ada@example.com", DisplayName: "Ada", Role: domain.RoleMember, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Wednesday, August 5, 2026: last week is July 27 - August 3.
	now := time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)
	lastWeek := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	if err := expenses.CreateExpense(ctx, domain.ExpenseRecord{
		ID: "expense-1", OwnerID: "owner-1", Title: "Cinema", AmountMinor: 2500, Currency: "EUR",
		OccurredAt: lastWeek, CategoryID: "entertainment", CategoryName: "Entertainment", Status: domain.ExpenseConfirmed,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	generated, err := svc.GenerateDue(ctx, now)
	if err != nil {
		t.Fatalf("generate due: %v", err)
	}
	// One report "generated" per due period that produced at least one file;
	// week/month/year are all due the first time this runs.
	if generated != 3 {
		t.Fatalf("generated = %d, want 3 (week, month, year each due for the first time)", generated)
	}

	listed, err := svc.ListReports(ctx, "owner-1")
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if len(listed) != 6 {
		t.Fatalf("listed reports = %d, want 6 (3 periods x csv+pdf): %#v", len(listed), listed)
	}
	foundCSV, foundPDF := false, false
	for _, value := range listed {
		if value.PeriodKind != domain.AnalyticsWeek {
			continue
		}
		if value.Format == domain.ReportFormatCSV {
			foundCSV = true
		}
		if value.Format == domain.ReportFormatPDF {
			foundPDF = true
		}
		fetched, err := svc.GetReport(ctx, "owner-1", value.ID)
		if err != nil {
			t.Fatalf("get report %s: %v", value.ID, err)
		}
		if fetched.ObjectKey != value.ObjectKey {
			t.Fatalf("get report object key = %q, want %q", fetched.ObjectKey, value.ObjectKey)
		}
	}
	if !foundCSV || !foundPDF {
		t.Fatalf("weekly report missing a format: csv=%v pdf=%v", foundCSV, foundPDF)
	}

	keys := objects.Keys()
	if len(keys) != 6 {
		t.Fatalf("stored objects = %d, want 6: %#v", len(keys), keys)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "reports/owner-1/") {
			t.Fatalf("unexpected object key %q", key)
		}
	}

	if len(notifications.Deliveries()) != 3 {
		t.Fatalf("notifications = %d, want 3 (one per due period)", len(notifications.Deliveries()))
	}
	for _, delivery := range notifications.Deliveries() {
		if delivery.Kind != domain.NotificationReportReady || delivery.UserID != "owner-1" {
			t.Fatalf("unexpected delivery: %#v", delivery)
		}
	}

	// A second tick at the same instant must not regenerate or re-notify:
	// every due period was already produced above.
	generatedAgain, err := svc.GenerateDue(ctx, now)
	if err != nil {
		t.Fatalf("generate due (second tick): %v", err)
	}
	if generatedAgain != 0 {
		t.Fatalf("generated on repeat tick = %d, want 0", generatedAgain)
	}
	if len(notifications.Deliveries()) != 3 {
		t.Fatalf("notifications after repeat tick = %d, want still 3", len(notifications.Deliveries()))
	}
}

func TestReportServiceGenerateDueSkipsBlockedUsers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, identity, _, _ := newTestReportService(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "owner-1", Email: "blocked@example.com", DisplayName: "Blocked", Role: domain.RoleMember, AccessBlocked: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	generated, err := svc.GenerateDue(ctx, time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate due: %v", err)
	}
	if generated != 0 {
		t.Fatalf("generated = %d, want 0 for a blocked user", generated)
	}
}

func TestReportServiceGetReportIsOwnerScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, expenses, identity, _, _ := newTestReportService(t)
	if err := identity.CreateUser(ctx, domain.AppUser{ID: "owner-1", Email: "a@example.com", DisplayName: "A", Role: domain.RoleMember, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)
	if err := expenses.CreateExpense(ctx, domain.ExpenseRecord{
		ID: "expense-1", OwnerID: "owner-1", Title: "Cinema", AmountMinor: 2500, Currency: "EUR",
		OccurredAt: time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC), CategoryName: "Entertainment", Status: domain.ExpenseConfirmed,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := svc.GenerateDue(ctx, now); err != nil {
		t.Fatalf("generate due: %v", err)
	}
	listed, err := svc.ListReports(ctx, "owner-1")
	if err != nil || len(listed) == 0 {
		t.Fatalf("list reports: %#v, %v", listed, err)
	}
	if _, err := svc.GetReport(ctx, "someone-else", listed[0].ID); err != service.ErrNotFound {
		t.Fatalf("get report as wrong owner: err = %v, want ErrNotFound", err)
	}
}
