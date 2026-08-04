package report

import (
	"fmt"
	"io"

	"github.com/go-pdf/fpdf"
)

// WritePDF renders a cover section, a per-category totals table, and a
// per-expense detail table.
func WritePDF(w io.Writer, data Data) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()

	pdf.SetFont("Arial", "B", 16)
	title := fmt.Sprintf("Expense report - %s", data.PeriodKind)
	pdf.CellFormat(0, 10, title, "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "", 11)
	if data.OwnerName != "" {
		pdf.CellFormat(0, 7, data.OwnerName, "", 1, "L", false, 0, "")
	}
	period := fmt.Sprintf("Period: %s to %s", data.PeriodStart.Format("2006-01-02"), data.PeriodEnd.Format("2006-01-02"))
	pdf.CellFormat(0, 7, period, "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 7, "Generated: "+data.GeneratedAt.Format("2006-01-02 15:04 MST"), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	writeTotalsTable(pdf, data.Totals)
	pdf.Ln(6)
	writeDetailTable(pdf, data.Rows)

	return pdf.Output(w)
}

func writeTotalsTable(pdf *fpdf.Fpdf, totals []CategoryTotal) {
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 8, "Totals by category", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 10)
	widths := []float64{70, 30, 40, 30}
	headers := []string{"Category", "Currency", "Total", "Count"}
	for i, header := range headers {
		pdf.CellFormat(widths[i], 8, header, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Arial", "", 10)
	for _, total := range totals {
		pdf.CellFormat(widths[0], 8, total.Category, "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[1], 8, total.Currency, "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[2], 8, formatAmount(total.AmountMinor), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[3], 8, itoa(total.Count), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeDetailTable(pdf *fpdf.Fpdf, rows []ExpenseRow) {
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 8, "Expenses", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 10)
	widths := []float64{25, 55, 40, 25, 25}
	headers := []string{"Date", "Title", "Category", "Currency", "Amount"}
	for i, header := range headers {
		pdf.CellFormat(widths[i], 8, header, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Arial", "", 10)
	for _, row := range rows {
		pdf.CellFormat(widths[0], 8, row.OccurredAt.Format("2006-01-02"), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[1], 8, truncate(row.Title, 32), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[2], 8, truncate(row.Category, 22), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[3], 8, row.Currency, "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[4], 8, formatAmount(row.AmountMinor), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

// truncate shortens s to at most max runes, so a long title cannot overflow
// its fixed-width table cell.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
