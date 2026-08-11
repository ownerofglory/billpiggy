package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/report"
)

// reportPageSize bounds one ListExpenses page while a report collects a
// period's expenses. Generation loops pages until one comes back short.
const reportPageSize = 500

// ReportService generates and serves periodic CSV/PDF expense reports.
//
// Unlike most services, it takes no injectable clock: GenerateDue already
// takes now as an explicit parameter, since a scheduler calling it once per
// tick and a test calling it at a fixed instant both want the exact same
// value used throughout that call, not a fresh time.Now() read part-way
// through.
type ReportService struct {
	reports       outbound.ReportRepository
	expenses      outbound.ExpenseRepository
	taxonomy      outbound.TaxonomyRepository
	identity      outbound.IdentityRepository
	objects       outbound.ObjectStore
	notifications outbound.NotificationRepository
	ids           func() string
}

// NewReportService creates a report service.
func NewReportService(reports outbound.ReportRepository, expenses outbound.ExpenseRepository, taxonomy outbound.TaxonomyRepository, identity outbound.IdentityRepository, objects outbound.ObjectStore, notifications outbound.NotificationRepository) (*ReportService, error) {
	if reports == nil || expenses == nil || taxonomy == nil || identity == nil || objects == nil || notifications == nil {
		return nil, errors.New("report, expense, taxonomy, identity, object, and notification dependencies are required")
	}
	return &ReportService{reports: reports, expenses: expenses, taxonomy: taxonomy, identity: identity, objects: objects, notifications: notifications, ids: uuid.NewString}, nil
}

// ListReports lists the current user's generated reports, newest first.
func (s *ReportService) ListReports(ctx context.Context, ownerID string) ([]domain.Report, error) {
	return s.reports.ListReports(ctx, ownerID)
}

// GetReport returns one owner-scoped report for download.
func (s *ReportService) GetReport(ctx context.Context, ownerID, reportID string) (domain.Report, error) {
	value, err := s.reports.GetReport(ctx, ownerID, reportID)
	if err != nil {
		return domain.Report{}, ErrNotFound
	}
	return value, nil
}

// periodKey identifies one generated report for the due-period skip check.
// PeriodStart compares by Unix second rather than by time.Time equality,
// which is sensitive to monotonic readings and location that never survive a
// database round trip anyway.
type periodKey struct {
	kind        domain.AnalyticsPeriod
	periodStart int64
	format      domain.ReportFormat
}

func keyFor(r domain.Report) periodKey {
	return periodKey{kind: r.PeriodKind, periodStart: r.PeriodStart.UTC().Unix(), format: r.Format}
}

// GenerateDue generates every weekly/monthly/yearly report that has become
// due (its period has fully elapsed as of now) and has not already been
// generated, for every user with access.
//
// One user's failure does not stop the run: errors are collected and
// returned together so a scheduler can log them, while every other user
// still gets a chance to generate their due reports this tick.
func (s *ReportService) GenerateDue(ctx context.Context, now time.Time) (int, error) {
	users, err := s.identity.ListUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("list users: %w", err)
	}
	var errs []error
	generated := 0
	for _, user := range users {
		if user.AccessBlocked {
			continue
		}
		existing, err := s.reports.ListReports(ctx, user.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("list reports for %s: %w", user.ID, err))
			continue
		}
		done := make(map[periodKey]bool, len(existing))
		for _, value := range existing {
			done[keyFor(value)] = true
		}
		for _, kind := range domain.ReportPeriods() {
			start := domain.LastCompletedReportPeriod(kind, now)
			if done[periodKey{kind, start.UTC().Unix(), domain.ReportFormatCSV}] && done[periodKey{kind, start.UTC().Unix(), domain.ReportFormatPDF}] {
				continue
			}
			count, err := s.generatePeriod(ctx, user, kind, start, done, now)
			if err != nil {
				errs = append(errs, fmt.Errorf("generate %s report for %s: %w", kind, user.ID, err))
				continue
			}
			generated += count
		}
	}
	return generated, errors.Join(errs...)
}

// generatePeriod renders and stores whichever formats of one owner's period
// report are still missing, and queues one report_ready notification when it
// creates at least one of them.
func (s *ReportService) generatePeriod(ctx context.Context, user domain.AppUser, kind domain.AnalyticsPeriod, start time.Time, done map[periodKey]bool, now time.Time) (int, error) {
	end := domain.ReportPeriodEnd(kind, start)
	rows, totals, categoryColors, err := s.collect(ctx, user.ID, start, end)
	if err != nil {
		return 0, fmt.Errorf("collect expenses: %w", err)
	}
	data := report.Data{
		PeriodKind: string(kind), PeriodStart: start, PeriodEnd: end, GeneratedAt: now,
		OwnerName: user.DisplayName, Rows: rows, Totals: totals, CategoryColors: categoryColors,
	}
	createdAny := false
	for _, format := range []domain.ReportFormat{domain.ReportFormatCSV, domain.ReportFormatPDF} {
		key := periodKey{kind, start.UTC().Unix(), format}
		if done[key] {
			continue
		}
		body, extension, contentType, err := render(format, data)
		if err != nil {
			return 0, fmt.Errorf("render %s report: %w", format, err)
		}
		objectKey := fmt.Sprintf("reports/%s/%s/%s.%s", user.ID, kind, start.UTC().Format("2006-01-02"), extension)
		if err := s.objects.Put(ctx, objectKey, bytes.NewReader(body), int64(len(body)), contentType); err != nil {
			return 0, fmt.Errorf("store report: %w", err)
		}
		record := domain.Report{ID: s.ids(), OwnerID: user.ID, PeriodKind: kind, PeriodStart: start, Format: format, ObjectKey: objectKey, CreatedAt: now}
		if err := s.reports.CreateReport(ctx, record); err != nil {
			// Another replica's tick already generated this report between
			// our existence check and now; nothing left to do.
			if errors.Is(err, outbound.ErrReportExists) {
				continue
			}
			return 0, fmt.Errorf("save report: %w", err)
		}
		createdAny = true
	}
	if !createdAny {
		return 0, nil
	}
	if err := s.queueReady(ctx, user.ID, kind, start, now); err != nil {
		return 0, err
	}
	return 1, nil
}

// collect gathers every expense in [start, end) and the per-category totals
// they imply, resolving tag ids to their display names and category names to
// the same hex colors the app itself shows for them.
func (s *ReportService) collect(ctx context.Context, ownerID string, start, end time.Time) ([]report.ExpenseRow, []report.CategoryTotal, map[string]string, error) {
	tags, err := s.taxonomy.ListTags(ctx, ownerID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list tags: %w", err)
	}
	tagNames := make(map[string]string, len(tags))
	for _, tag := range tags {
		tagNames[tag.ID] = tag.Name
	}
	categories, err := s.taxonomy.ListCategories(ctx, ownerID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list categories: %w", err)
	}
	categoryColors := make(map[string]string, len(categories))
	for _, category := range categories {
		if category.Color != "" {
			categoryColors[category.Name] = category.Color
		}
	}

	rows := make([]report.ExpenseRow, 0)
	type totalKey struct{ category, currency string }
	totals := map[totalKey]*report.CategoryTotal{}
	offset := 0
	for {
		expenses, err := s.expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: ownerID, From: start, To: end, Limit: reportPageSize, Offset: offset})
		if err != nil {
			return nil, nil, nil, err
		}
		for _, expense := range expenses {
			category := expense.CategoryName
			if category == "" {
				category = "Uncategorized"
			}
			tagLabels := make([]string, 0, len(expense.TagIDs))
			for _, tagID := range expense.TagIDs {
				if name, ok := tagNames[tagID]; ok {
					tagLabels = append(tagLabels, name)
				}
			}
			rows = append(rows, report.ExpenseRow{
				OccurredAt: expense.OccurredAt, Title: expense.Title, Category: category,
				Currency: expense.Currency, AmountMinor: expense.AmountMinor, Tags: tagLabels,
			})
			key := totalKey{category, expense.Currency}
			total, ok := totals[key]
			if !ok {
				total = &report.CategoryTotal{Category: category, Currency: expense.Currency}
				totals[key] = total
			}
			total.AmountMinor += expense.AmountMinor
			total.Count++
		}
		if len(expenses) < reportPageSize {
			break
		}
		offset += reportPageSize
	}
	values := make([]report.CategoryTotal, 0, len(totals))
	for _, total := range totals {
		values = append(values, *total)
	}
	return rows, values, categoryColors, nil
}

// queueReady enqueues the report-ready notification, writing through the
// ordinary notification port exactly as the budget-alert projection does.
func (s *ReportService) queueReady(ctx context.Context, ownerID string, kind domain.AnalyticsPeriod, start, now time.Time) error {
	delivery := domain.NotificationDelivery{
		ID:     s.ids(),
		UserID: ownerID,
		Kind:   domain.NotificationReportReady,
		Payload: map[string]string{
			"period_kind":  string(kind),
			"period_start": start.Format(time.RFC3339),
		},
		CreatedAt: now.UTC(),
		Status:    domain.NotificationPending,
	}
	if err := s.notifications.QueueNotification(ctx, delivery); err != nil {
		return fmt.Errorf("queue report ready notification: %w", err)
	}
	return nil
}

// render dispatches to the CSV or PDF renderer and returns its bytes, file
// extension, and content type.
func render(format domain.ReportFormat, data report.Data) ([]byte, string, string, error) {
	var buf bytes.Buffer
	switch format {
	case domain.ReportFormatCSV:
		if err := report.WriteCSV(&buf, data); err != nil {
			return nil, "", "", err
		}
		return buf.Bytes(), "csv", "text/csv", nil
	case domain.ReportFormatPDF:
		if err := report.WritePDF(&buf, data); err != nil {
			return nil, "", "", err
		}
		return buf.Bytes(), "pdf", "application/pdf", nil
	default:
		return nil, "", "", fmt.Errorf("unsupported report format %q", format)
	}
}
