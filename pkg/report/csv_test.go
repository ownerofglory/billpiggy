package report_test

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/pkg/report"
)

func TestWriteCSVListsRowsThenTotals(t *testing.T) {
	t.Parallel()
	data := report.Data{
		PeriodKind:  "week",
		PeriodStart: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		GeneratedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		Rows: []report.ExpenseRow{
			{OccurredAt: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), Title: "Cinema", Category: "Entertainment", Currency: "EUR", AmountMinor: 2500, Tags: []string{"family", "movie"}},
			{OccurredAt: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), Title: "Popcorn", Category: "Entertainment", Currency: "EUR", AmountMinor: 1100},
		},
		Totals: []report.CategoryTotal{
			{Category: "Entertainment", Currency: "EUR", AmountMinor: 3600, Count: 2},
		},
	}
	var buf bytes.Buffer
	if err := report.WriteCSV(&buf, data); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	reader := csv.NewReader(strings.NewReader(buf.String()))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	// csv.Reader silently skips blank lines, so the separator between the
	// expense rows and the totals section does not appear as its own record.
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5 (header + 2 rows + totals header + totals row): %#v", len(records), records)
	}
	if got := records[1]; got[1] != "Cinema" || got[4] != "25.00" || got[5] != "family;movie" {
		t.Fatalf("row 1 = %#v", got)
	}
	if got := records[2]; got[1] != "Popcorn" || got[4] != "11.00" || got[5] != "" {
		t.Fatalf("row 2 = %#v", got)
	}
	if got := records[4]; got[0] != "Entertainment" || got[2] != "36.00" || got[3] != "2" {
		t.Fatalf("totals row = %#v", got)
	}
}

func TestWriteCSVEmptyData(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := report.WriteCSV(&buf, report.Data{}); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if !strings.Contains(buf.String(), "date,title,category,currency,amount,tags") {
		t.Fatalf("missing header: %q", buf.String())
	}
}
