// Package report renders a period's expenses as CSV and PDF documents.
//
// Both renderers consume the same [Data], so the two formats never drift:
// whatever the CSV lists is exactly what the PDF summarises and details.
package report

import "time"

// ExpenseRow is one expense line in the reported period.
type ExpenseRow struct {
	OccurredAt  time.Time
	Title       string
	Category    string
	Currency    string
	AmountMinor int64
	Tags        []string
}

// CategoryTotal is the aggregate spend for one category and currency.
type CategoryTotal struct {
	Category    string
	Currency    string
	AmountMinor int64
	Count       int
}

// Data is everything a renderer needs to produce one report.
type Data struct {
	// PeriodKind is a human label such as "week", "month", or "year".
	PeriodKind string
	// PeriodStart and PeriodEnd bound the reported half-open window.
	PeriodStart, PeriodEnd time.Time
	// GeneratedAt records when the report was produced.
	GeneratedAt time.Time
	// OwnerName is shown on the PDF cover section; optional.
	OwnerName string
	// Rows lists every expense in the period, most recent first.
	Rows []ExpenseRow
	// Totals summarises spend per category and currency.
	Totals []CategoryTotal
	// CategoryColors optionally maps a category name to its hex color, the
	// same swatch the app itself shows for that category. A category with no
	// entry (a custom category with no color set, or "Uncategorized") falls
	// back to a generated color so charts always render something reasonable
	// rather than a blank or erroring one.
	CategoryColors map[string]string
}
