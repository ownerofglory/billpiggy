package domain

import "time"

// ReportFormat is the file format a generated report was rendered in.
type ReportFormat string

const (
	// ReportFormatCSV is a per-expense comma-separated export.
	ReportFormatCSV ReportFormat = "csv"
	// ReportFormatPDF is a formatted summary-and-detail document.
	ReportFormatPDF ReportFormat = "pdf"
)

// Report is a generated periodic expense report stored in object storage.
type Report struct {
	// ID identifies the report.
	ID string `json:"id"`
	// OwnerID is the user the report was generated for.
	OwnerID string `json:"ownerID"`
	// PeriodKind is the report's granularity: week, month, or year.
	PeriodKind AnalyticsPeriod `json:"periodKind"`
	// PeriodStart is the inclusive start of the reported period.
	PeriodStart time.Time `json:"periodStart"`
	// Format is the file format the report was rendered in.
	Format ReportFormat `json:"format"`
	// ObjectKey is the report's location in object storage.
	ObjectKey string `json:"objectKey"`
	// CreatedAt is when the report was generated.
	CreatedAt time.Time `json:"createdAt"`
}

// ReportPeriods lists the granularities periodic reports are generated for.
func ReportPeriods() []AnalyticsPeriod {
	return []AnalyticsPeriod{AnalyticsWeek, AnalyticsMonth, AnalyticsYear}
}

// ValidReportPeriod reports whether kind is a valid report granularity.
func ValidReportPeriod(kind AnalyticsPeriod) bool {
	for _, valid := range ReportPeriods() {
		if kind == valid {
			return true
		}
	}
	return false
}

// LastCompletedReportPeriod returns the start of the most recently completed
// period of kind, strictly before now. A weekly report becomes due once the
// prior calendar week (Monday-start, matching RollupPeriodStart) has fully
// elapsed, and likewise for month and year.
func LastCompletedReportPeriod(kind AnalyticsPeriod, now time.Time) time.Time {
	currentStart := RollupPeriodStart(kind, now)
	switch kind {
	case AnalyticsWeek:
		return currentStart.AddDate(0, 0, -7)
	case AnalyticsMonth:
		return currentStart.AddDate(0, -1, 0)
	case AnalyticsYear:
		return currentStart.AddDate(-1, 0, 0)
	default:
		return currentStart
	}
}

// ReportPeriodEnd returns the exclusive end of the report period of kind that
// starts at start.
func ReportPeriodEnd(kind AnalyticsPeriod, start time.Time) time.Time {
	switch kind {
	case AnalyticsWeek:
		return start.AddDate(0, 0, 7)
	case AnalyticsMonth:
		return start.AddDate(0, 1, 0)
	case AnalyticsYear:
		return start.AddDate(1, 0, 0)
	default:
		return start
	}
}
