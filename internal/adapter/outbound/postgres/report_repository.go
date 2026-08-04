package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

const reportColumns = `id::text,owner_id::text,period_kind,period_start,format,object_key,created_at`

func scanReport(row pgx.Row) (domain.Report, error) {
	var report domain.Report
	var periodKind, format string
	err := row.Scan(&report.ID, &report.OwnerID, &periodKind, &report.PeriodStart, &format, &report.ObjectKey, &report.CreatedAt)
	report.PeriodKind = domain.AnalyticsPeriod(periodKind)
	report.Format = domain.ReportFormat(format)
	return report, err
}

// ReportRepository persists the generated-report projection in PostgreSQL.
type ReportRepository struct{ pool *pgxpool.Pool }

// NewReportRepository creates a PostgreSQL report adapter.
func NewReportRepository(pool *pgxpool.Pool) *ReportRepository { return &ReportRepository{pool: pool} }

// CreateReport records a newly generated report. The unique constraint on
// (owner_id, period_kind, period_start, format) is the sole coordination
// mechanism between replicas that both attempt to generate the same report:
// whichever insert lands first wins, and the other observes ErrReportExists.
func (r *ReportRepository) CreateReport(ctx context.Context, report domain.Report) error {
	tag, err := pgxtx.From(ctx, r.pool).Exec(ctx,
		`insert into reports.reports(id,owner_id,period_kind,period_start,format,object_key,created_at)
		 values($1,$2,$3,$4,$5,$6,$7)
		 on conflict (owner_id, period_kind, period_start, format) do nothing`,
		report.ID, report.OwnerID, report.PeriodKind, report.PeriodStart, report.Format, report.ObjectKey, report.CreatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return outbound.ErrReportExists
	}
	return nil
}

// ListReports lists an owner's reports, newest first.
func (r *ReportRepository) ListReports(ctx context.Context, ownerID string) ([]domain.Report, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select `+reportColumns+` from reports.reports where owner_id=$1 order by created_at desc`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []domain.Report{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

// GetReport returns one owner-scoped report.
func (r *ReportRepository) GetReport(ctx context.Context, ownerID, reportID string) (domain.Report, error) {
	return scanReport(pgxtx.From(ctx, r.pool).QueryRow(ctx, `select `+reportColumns+` from reports.reports where id=$1 and owner_id=$2`, reportID, ownerID))
}
