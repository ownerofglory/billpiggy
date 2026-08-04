package outbound

import (
	"context"
	"errors"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ErrReportExists reports that a report for the same owner, period, and
// format has already been generated. The scheduler treats this as success:
// another tick or replica already did the work.
var ErrReportExists = errors.New("report already exists")

// ReportRepository owns the generated-report projection.
type ReportRepository interface {
	// CreateReport records a newly generated report, returning ErrReportExists
	// when one already exists for the same owner, period, and format.
	CreateReport(ctx context.Context, report domain.Report) error
	// ListReports lists an owner's reports, newest first.
	ListReports(ctx context.Context, ownerID string) ([]domain.Report, error)
	// GetReport returns one owner-scoped report.
	GetReport(ctx context.Context, ownerID, reportID string) (domain.Report, error)
}
