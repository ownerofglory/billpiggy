package report_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/report"
)

func TestWritePDFProducesAValidDocument(t *testing.T) {
	t.Parallel()
	data := report.Data{
		PeriodKind:  "month",
		PeriodStart: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		GeneratedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		OwnerName:   "Ada Lovelace",
		Rows: []report.ExpenseRow{
			{OccurredAt: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Title: "Cinema", Category: "Entertainment", Currency: "EUR", AmountMinor: 2500},
		},
		Totals: []report.CategoryTotal{
			{Category: "Entertainment", Currency: "EUR", AmountMinor: 2500, Count: 1},
		},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, data); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty pdf output")
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatalf("output does not start with a PDF header: %q", buf.Bytes()[:min(20, buf.Len())])
	}
	if !bytes.Contains(buf.Bytes(), []byte("%%EOF")) {
		t.Fatal("output missing PDF trailer")
	}
}

func TestWritePDFRendersChartsWithMixedCategoriesAndCurrencies(t *testing.T) {
	t.Parallel()
	data := report.Data{
		PeriodKind:  "month",
		PeriodStart: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		GeneratedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		OwnerName:   "Ada Lovelace",
		Rows: []report.ExpenseRow{
			{OccurredAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), Title: "Groceries", Category: "Groceries", Currency: "EUR", AmountMinor: 4200},
			{OccurredAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), Title: "Cinema", Category: "Entertainment", Currency: "EUR", AmountMinor: 2500},
			// A custom category with no assigned color must still render —
			// via the fallback palette, not an error or a blank chart.
			{OccurredAt: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Title: "Yoga", Category: "Wellness", Currency: "EUR", AmountMinor: 1800},
			// A second currency must not be mixed into the same ring/stack.
			{OccurredAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), Title: "Hotel", Category: "Transport", Currency: "USD", AmountMinor: 9000},
		},
		Totals: []report.CategoryTotal{
			{Category: "Groceries", Currency: "EUR", AmountMinor: 4200, Count: 1},
			{Category: "Entertainment", Currency: "EUR", AmountMinor: 2500, Count: 1},
			{Category: "Wellness", Currency: "EUR", AmountMinor: 1800, Count: 1},
			{Category: "Transport", Currency: "USD", AmountMinor: 9000, Count: 1},
		},
		CategoryColors: map[string]string{"Groceries": "#84cc16", "Entertainment": "#ec4899"},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, data); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) || !bytes.Contains(buf.Bytes(), []byte("%%EOF")) {
		t.Fatal("output is not a well-formed PDF")
	}
}

func TestWritePDFHandlesNoExpenses(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	data := report.Data{PeriodKind: "week", PeriodStart: time.Now(), PeriodEnd: time.Now(), GeneratedAt: time.Now()}
	if err := report.WritePDF(&buf, data); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output does not start with a PDF header")
	}
}
