//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

func TestReportRepositoryCreateListGet(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewReportRepository(pool)
	owner := seedUser(t, pool, "reports-owner@example.test")
	ctx := context.Background()
	periodStart := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)

	csvReport := domain.Report{ID: uuid.NewString(), OwnerID: owner, PeriodKind: domain.AnalyticsWeek, PeriodStart: periodStart, Format: domain.ReportFormatCSV, ObjectKey: "reports/" + owner + "/week/2026-07-27.csv", CreatedAt: time.Now()}
	if err := repository.CreateReport(ctx, csvReport); err != nil {
		t.Fatalf("create csv report: %v", err)
	}
	pdfReport := domain.Report{ID: uuid.NewString(), OwnerID: owner, PeriodKind: domain.AnalyticsWeek, PeriodStart: periodStart, Format: domain.ReportFormatPDF, ObjectKey: "reports/" + owner + "/week/2026-07-27.pdf", CreatedAt: time.Now()}
	if err := repository.CreateReport(ctx, pdfReport); err != nil {
		t.Fatalf("create pdf report: %v", err)
	}

	listed, err := repository.ListReports(ctx, owner)
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %d, want 2 (csv + pdf)", len(listed))
	}

	fetched, err := repository.GetReport(ctx, owner, csvReport.ID)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}
	if fetched.ObjectKey != csvReport.ObjectKey || fetched.Format != domain.ReportFormatCSV || !fetched.PeriodStart.Equal(periodStart) {
		t.Fatalf("fetched report = %#v", fetched)
	}

	if _, err := repository.GetReport(ctx, seedUser(t, pool, "other-owner@example.test"), csvReport.ID); err == nil {
		t.Fatal("expected an error fetching another owner's report")
	}
}

func TestReportRepositoryCreateReportConflictsOnSamePeriodAndFormat(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewReportRepository(pool)
	owner := seedUser(t, pool, "reports-conflict@example.test")
	ctx := context.Background()
	periodStart := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)

	first := domain.Report{ID: uuid.NewString(), OwnerID: owner, PeriodKind: domain.AnalyticsMonth, PeriodStart: periodStart, Format: domain.ReportFormatCSV, ObjectKey: "reports/" + owner + "/month/2026-07-01.csv", CreatedAt: time.Now()}
	if err := repository.CreateReport(ctx, first); err != nil {
		t.Fatalf("create first report: %v", err)
	}

	// A second replica racing to generate the same owner/period/format must
	// observe ErrReportExists rather than a duplicate row or a raw constraint
	// violation, and the first report's object key must be left untouched.
	second := first
	second.ID = uuid.NewString()
	second.ObjectKey = "reports/" + owner + "/month/2026-07-01-retry.csv"
	if err := repository.CreateReport(ctx, second); !errors.Is(err, outbound.ErrReportExists) {
		t.Fatalf("create conflicting report: err = %v, want ErrReportExists", err)
	}

	fetched, err := repository.GetReport(ctx, owner, first.ID)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}
	if fetched.ObjectKey != first.ObjectKey {
		t.Fatalf("object key = %q, want unchanged %q", fetched.ObjectKey, first.ObjectKey)
	}

	// A different format for the same period is independent and must succeed.
	pdf := first
	pdf.ID = uuid.NewString()
	pdf.Format = domain.ReportFormatPDF
	pdf.ObjectKey = "reports/" + owner + "/month/2026-07-01.pdf"
	if err := repository.CreateReport(ctx, pdf); err != nil {
		t.Fatalf("create pdf report for same period: %v", err)
	}
}
