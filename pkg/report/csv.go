package report

import (
	"encoding/csv"
	"io"
	"strings"
)

// WriteCSV renders one expense per row, most recent first, followed by a
// blank separator line and a per-category total section.
func WriteCSV(w io.Writer, data Data) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"date", "title", "category", "currency", "amount", "tags"}); err != nil {
		return err
	}
	for _, row := range data.Rows {
		record := []string{
			row.OccurredAt.Format("2006-01-02"),
			row.Title,
			row.Category,
			row.Currency,
			formatAmount(row.AmountMinor),
			strings.Join(row.Tags, ";"),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	if err := writer.Write([]string{}); err != nil {
		return err
	}
	if err := writer.Write([]string{"category", "currency", "total", "count"}); err != nil {
		return err
	}
	for _, total := range data.Totals {
		record := []string{total.Category, total.Currency, formatAmount(total.AmountMinor), itoa(total.Count)}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
