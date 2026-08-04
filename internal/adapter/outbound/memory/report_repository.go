package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// reportKey identifies one generated report, matching the PostgreSQL unique
// constraint on (owner_id, period_kind, period_start, format).
type reportKey struct {
	ownerID     string
	periodKind  domain.AnalyticsPeriod
	periodStart int64
	format      domain.ReportFormat
}

// ReportRepository is an in-memory generated-report projection.
type ReportRepository struct {
	mu      sync.RWMutex
	reports map[string]domain.Report
	keys    map[reportKey]struct{}
}

// NewReportRepository creates an empty in-memory report projection.
func NewReportRepository() *ReportRepository {
	return &ReportRepository{reports: map[string]domain.Report{}, keys: map[reportKey]struct{}{}}
}

// CreateReport records a newly generated report.
func (r *ReportRepository) CreateReport(_ context.Context, report domain.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := reportKey{ownerID: report.OwnerID, periodKind: report.PeriodKind, periodStart: report.PeriodStart.UnixNano(), format: report.Format}
	if _, exists := r.keys[key]; exists {
		return outbound.ErrReportExists
	}
	r.keys[key] = struct{}{}
	r.reports[report.ID] = report
	return nil
}

// ListReports lists an owner's reports, newest first.
func (r *ReportRepository) ListReports(_ context.Context, ownerID string) ([]domain.Report, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.Report, 0)
	for _, report := range r.reports {
		if report.OwnerID == ownerID {
			values = append(values, report)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.After(values[j].CreatedAt) })
	return values, nil
}

// GetReport returns one owner-scoped report.
func (r *ReportRepository) GetReport(_ context.Context, ownerID, reportID string) (domain.Report, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	report, ok := r.reports[reportID]
	if !ok || report.OwnerID != ownerID {
		return domain.Report{}, errNotFound
	}
	return report, nil
}

// Snapshot copies the projection and returns a function restoring it.
func (r *ReportRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reports := make(map[string]domain.Report, len(r.reports))
	for id, value := range r.reports {
		reports[id] = value
	}
	keys := make(map[reportKey]struct{}, len(r.keys))
	for key := range r.keys {
		keys[key] = struct{}{}
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reports, r.keys = reports, keys
	}
}
