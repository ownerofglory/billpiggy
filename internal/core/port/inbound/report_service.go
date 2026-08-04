package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ReportService is everything an HTTP handler needs from generated-report
// queries. GenerateDue is wiring-only and excluded: only cmd/billpiggy's
// scheduler calls it.
type ReportService interface {
	ListReports(ctx context.Context, ownerID string) ([]domain.Report, error)
	GetReport(ctx context.Context, ownerID, reportID string) (domain.Report, error)
}
