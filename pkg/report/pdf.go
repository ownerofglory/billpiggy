package report

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// ---------------------------------------------------------------------------
// Brand tokens — the same RGB values as src/styles/index.css's custom
// properties in the frontend (and the transactional email templates), given
// here as 0-255 triplets since that's what fpdf's SetFillColor/SetTextColor/
// SetDrawColor take. DESIGN.md's "no heavy borders" rule becomes a light
// hairline divider rather than fpdf's default full black grid.
// ---------------------------------------------------------------------------

type rgb struct{ r, g, b int }

var (
	colorHeaderBG = rgb{18, 18, 18}    // obsidian header band
	colorInk      = rgb{24, 24, 27}    // primary body text
	colorInkMuted = rgb{82, 82, 91}    // secondary/meta text
	colorHairline = rgb{225, 225, 228} // row dividers, not a full grid
	colorBlueInk  = rgb{47, 111, 184}  // AA-legible primary-blue for text-on-white
	colorBlueTint = rgb{231, 240, 250} // table header fill
	colorWhite    = rgb{255, 255, 255}
)

func setFill(pdf *fpdf.Fpdf, c rgb) { pdf.SetFillColor(c.r, c.g, c.b) }
func setText(pdf *fpdf.Fpdf, c rgb) { pdf.SetTextColor(c.r, c.g, c.b) }
func setDraw(pdf *fpdf.Fpdf, c rgb) { pdf.SetDrawColor(c.r, c.g, c.b) }

const iconName = "billpiggy-icon"

// WritePDF renders a cover section, a per-category totals table, and a
// per-expense detail table, styled to match BillPiggy's brand.
func WritePDF(w io.Writer, data Data) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 20)

	// fpdf's core fonts (Arial/Helvetica etc.) expect cp1252-encoded text,
	// not UTF-8 — without this, anything outside plain ASCII (the em dash
	// and middot below, but just as much an accented expense title or
	// category name a user actually typed) comes out as mojibake instead of
	// erroring, so it's easy to miss until real data hits it.
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	pdf.SetFooterFunc(func() { writeFooter(pdf, tr) })
	registerIcon(pdf)
	pdf.AddPage()

	writeCover(pdf, tr, data)
	writePieChart(pdf, tr, data.Totals, data.CategoryColors)
	writeBarChart(pdf, tr, data, data.CategoryColors)
	writeTotalsTable(pdf, tr, data.Totals)
	pdf.Ln(6)
	writeDetailTable(pdf, tr, data.Rows)

	return pdf.Output(w)
}

// registerIcon loads the same "savings" mark used in the app sidebar and the
// transactional emails (Material Symbols Outlined, coral #f58a7a, rasterized
// to a PNG since fpdf can't render icon fonts) so the report's header band
// matches the rest of BillPiggy's brand surfaces. Registration is separate
// from placement so a failure here degrades to a text-only header rather
// than aborting the whole report.
func registerIcon(pdf *fpdf.Fpdf) {
	raw, err := base64.StdEncoding.DecodeString(iconBase64)
	if err != nil {
		return
	}
	pdf.RegisterImageOptionsReader(iconName, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(raw))
}

func writeCover(pdf *fpdf.Fpdf, tr func(string) string, data Data) {
	pageW, _ := pdf.GetPageSize()
	const bandH = 22.0

	// Dark "obsidian" band across the full page width, matching the app
	// shell's header/sidebar and the transactional emails' brand header.
	setFill(pdf, colorHeaderBG)
	pdf.Rect(0, 0, pageW, bandH, "F")

	if pdf.GetImageInfo(iconName) != nil {
		pdf.ImageOptions(iconName, 15, 5, 0, 12, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
		pdf.SetXY(28, 0)
	} else {
		pdf.SetXY(15, 0)
	}
	pdf.SetFont("Arial", "B", 14)
	setText(pdf, colorWhite)
	pdf.CellFormat(0, bandH, "BillPiggy", "", 0, "L", false, 0, "")

	pdf.SetY(bandH + 8)
	pdf.SetX(15)
	pdf.SetFont("Arial", "B", 18)
	setText(pdf, colorInk)
	title := fmt.Sprintf("Expense report — %s", data.PeriodKind)
	pdf.CellFormat(0, 9, tr(title), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	setText(pdf, colorInkMuted)
	if data.OwnerName != "" {
		pdf.SetX(15)
		pdf.CellFormat(0, 6, tr(data.OwnerName), "", 1, "L", false, 0, "")
	}
	pdf.SetX(15)
	period := fmt.Sprintf("Period: %s to %s", data.PeriodStart.Format("2006-01-02"), data.PeriodEnd.Format("2006-01-02"))
	pdf.CellFormat(0, 6, period, "", 1, "L", false, 0, "")
	pdf.SetX(15)
	pdf.CellFormat(0, 6, "Generated: "+data.GeneratedAt.Format("2006-01-02 15:04 MST"), "", 1, "L", false, 0, "")
	pdf.Ln(8)
}

// sectionHeading draws a bold title with a short primary-blue accent rule
// beneath it, echoing the badge/accent treatment used elsewhere in the
// brand rather than the plain unstyled headings the previous version used.
func sectionHeading(pdf *fpdf.Fpdf, title string) {
	pdf.SetFont("Arial", "B", 13)
	setText(pdf, colorInk)
	pdf.CellFormat(0, 8, title, "", 1, "L", false, 0, "")
	x, y := pdf.GetX(), pdf.GetY()
	setDraw(pdf, colorBlueInk)
	pdf.SetLineWidth(0.6)
	pdf.Line(x, y, x+18, y)
	pdf.Ln(4)
}

// ensureSpace starts a new page when less than height remains before the
// bottom margin. Needed specifically around the chart sections: unlike
// CellFormat/MultiCell, fpdf's raw shape primitives (Rect, Polygon, Line)
// never trigger its automatic page break, so a chart drawn too close to the
// bottom would otherwise render partly off the page instead of moving to a
// fresh one.
func ensureSpace(pdf *fpdf.Fpdf, height float64) {
	_, pageH := pdf.GetPageSize()
	_, _, _, bottom := pdf.GetMargins()
	if pdf.GetY()+height > pageH-bottom {
		pdf.AddPage()
	}
}

func tableHeaderRow(pdf *fpdf.Fpdf, widths []float64, headers []string, aligns []string) {
	pdf.SetFont("Arial", "B", 9)
	setFill(pdf, colorBlueTint)
	setText(pdf, colorBlueInk)
	pdf.SetDrawColor(colorBlueTint.r, colorBlueTint.g, colorBlueTint.b)
	for i, header := range headers {
		pdf.CellFormat(widths[i], 8, header, "", 0, aligns[i], true, 0, "")
	}
	pdf.Ln(-1)
}

// tableRow draws one hairline-bottomed row, alternating a very light zebra
// fill so long tables stay easy to scan without the heavy black grid fpdf
// draws by default — DESIGN.md calls that out explicitly as the "No-Line
// Rule": sectioning via subtle color shifts, not 1px borders. cells is
// passed pre-translated (tr already applied) since not every cell — dates,
// currency codes — needs it and the caller knows which do.
func tableRow(pdf *fpdf.Fpdf, widths []float64, cells []string, aligns []string, zebra bool) {
	pdf.SetFont("Arial", "", 9)
	setText(pdf, colorInk)
	setDraw(pdf, colorHairline)
	pdf.SetLineWidth(0.2)
	fill := false
	if zebra {
		setFill(pdf, rgb{250, 250, 251})
		fill = true
	}
	for i, cell := range cells {
		pdf.CellFormat(widths[i], 8, cell, "B", 0, aligns[i], fill, 0, "")
	}
	pdf.Ln(-1)
}

func writeTotalsTable(pdf *fpdf.Fpdf, tr func(string) string, totals []CategoryTotal) {
	sectionHeading(pdf, "Totals by category")

	widths := []float64{70, 30, 40, 30}
	headers := []string{"Category", "Currency", "Total", "Count"}
	aligns := []string{"L", "L", "R", "R"}
	tableHeaderRow(pdf, widths, headers, aligns)

	for i, total := range totals {
		tableRow(pdf, widths, []string{
			tr(total.Category),
			total.Currency,
			formatAmount(total.AmountMinor),
			itoa(total.Count),
		}, aligns, i%2 == 1)
	}
}

func writeDetailTable(pdf *fpdf.Fpdf, tr func(string) string, rows []ExpenseRow) {
	sectionHeading(pdf, "Expenses")

	widths := []float64{25, 55, 40, 25, 25}
	headers := []string{"Date", "Title", "Category", "Currency", "Amount"}
	aligns := []string{"L", "L", "L", "L", "R"}
	tableHeaderRow(pdf, widths, headers, aligns)

	for i, row := range rows {
		tableRow(pdf, widths, []string{
			row.OccurredAt.Format("2006-01-02"),
			tr(truncate(row.Title, 32)),
			tr(truncate(row.Category, 22)),
			row.Currency,
			formatAmount(row.AmountMinor),
		}, aligns, i%2 == 1)
	}
}

// ---------------------------------------------------------------------------
// Charts — a donut of spend by category and a stacked bar of spend over
// time. Both color by the app's own category colors (ExpenseCategory.Color,
// passed in as Data.CategoryColors) rather than an arbitrary new palette, so
// a category reads as the same color in the report as it does in the app.
// fpdf has no chart primitives at all: a donut wedge is a filled polygon
// approximating an arc, and a bar is just a plain filled Rect.
// ---------------------------------------------------------------------------

// fallbackPalette colors a category with no assigned color (a custom
// category that never got one, or the synthetic "Uncategorized" bucket).
// Chosen by a hash of the category name rather than by position, so the same
// category gets the same fallback color every time it's rendered, including
// consistently between the pie and bar charts in one report.
var fallbackPalette = []rgb{
	{100, 116, 139}, // slate
	{234, 179, 8},   // amber
	{6, 182, 212},   // cyan
	{168, 85, 247},  // violet
	{34, 197, 94},   // emerald
	{244, 63, 94},   // rose
}

func categoryColor(name string, colors map[string]string) rgb {
	if hex, ok := colors[name]; ok {
		if c, ok := parseHexColor(hex); ok {
			return c
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fallbackPalette[h.Sum32()%uint32(len(fallbackPalette))]
}

func parseHexColor(hex string) (rgb, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return rgb{}, false
	}
	value, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{r: int(value >> 16 & 0xff), g: int(value >> 8 & 0xff), b: int(value & 0xff)}, true
}

// dominantCurrency returns the currency with the greatest total spend in
// totals, and every other currency present. A chart can only meaningfully
// show one currency at a time — summing across currencies without a
// conversion rate would misrepresent the total — so the less-common ones are
// named in a caption instead of being silently mixed in or silently dropped.
func dominantCurrency(totals []CategoryTotal) (string, []string) {
	sums := map[string]int64{}
	for _, total := range totals {
		sums[total.Currency] += total.AmountMinor
	}
	var dominant string
	var max int64 = -1
	for currency, sum := range sums {
		if sum > max {
			dominant, max = currency, sum
		}
	}
	others := make([]string, 0, len(sums))
	for currency := range sums {
		if currency != dominant {
			others = append(others, currency)
		}
	}
	sort.Strings(others)
	return dominant, others
}

type legendItem struct {
	label, value string
	color        rgb
}

// drawLegend lists each item on its own line — a colored swatch, the label,
// and its amount right-aligned — rather than a wrapped horizontal strip: it
// never needs to guess how many columns fit, and is easier to scan on a
// printed page than an interactive one. Returns the Y position after the
// last line, so the caller can advance past whichever of a chart and its
// legend ended up taller.
func drawLegend(pdf *fpdf.Fpdf, tr func(string) string, x, y, width float64, items []legendItem) float64 {
	const swatch, lineH, valueWidth = 3.2, 6.0, 32.0
	labelWidth := width - swatch - 3 - valueWidth
	pdf.SetFont("Arial", "", 9)
	for i, item := range items {
		rowY := y + float64(i)*lineH
		setFill(pdf, item.color)
		pdf.Rect(x, rowY+1.2, swatch, swatch, "F")
		pdf.SetXY(x+swatch+2, rowY)
		setText(pdf, colorInk)
		pdf.CellFormat(labelWidth, lineH, tr(item.label), "", 0, "L", false, 0, "")
		setText(pdf, colorInkMuted)
		pdf.CellFormat(valueWidth, lineH, item.value, "", 0, "R", false, 0, "")
	}
	return y + float64(len(items))*lineH
}

// drawDonutWedge fills the ring segment between innerR and outerR, sweeping
// from startDeg to endDeg (0° = 3 o'clock, increasing clockwise) as one
// filled polygon. fpdf has no arc or pie-slice primitive, so the arc is
// approximated by sampling points every 2° — closely enough that the facets
// are not visible at print resolution. The white stroke between wedges
// echoes the visible seam in the app's own donut chart.
func drawDonutWedge(pdf *fpdf.Fpdf, cx, cy, outerR, innerR, startDeg, endDeg float64, color rgb) {
	const stepDeg = 2.0
	steps := int(math.Ceil((endDeg - startDeg) / stepDeg))
	if steps < 1 {
		steps = 1
	}
	points := make([]fpdf.PointType, 0, steps*2+2)
	for i := 0; i <= steps; i++ {
		angle := (startDeg + (endDeg-startDeg)*float64(i)/float64(steps)) * math.Pi / 180
		points = append(points, fpdf.PointType{X: cx + outerR*math.Cos(angle), Y: cy + outerR*math.Sin(angle)})
	}
	for i := steps; i >= 0; i-- {
		angle := (startDeg + (endDeg-startDeg)*float64(i)/float64(steps)) * math.Pi / 180
		points = append(points, fpdf.PointType{X: cx + innerR*math.Cos(angle), Y: cy + innerR*math.Sin(angle)})
	}
	setFill(pdf, color)
	setDraw(pdf, colorWhite)
	pdf.SetLineWidth(0.4)
	pdf.Polygon(points, "FD")
}

// writePieChart draws a donut of spend by category for the dominant
// currency, with a legend giving every category's exact amount and expense
// count. A no-op when there is nothing to chart, so a period with zero
// expenses does not draw an empty ring.
func writePieChart(pdf *fpdf.Fpdf, tr func(string) string, totals []CategoryTotal, colors map[string]string) {
	if len(totals) == 0 {
		return
	}
	ensureSpace(pdf, 75)
	sectionHeading(pdf, "Spending by category")

	currency, others := dominantCurrency(totals)
	filtered := make([]CategoryTotal, 0, len(totals))
	var sum int64
	for _, total := range totals {
		if total.Currency != currency {
			continue
		}
		filtered = append(filtered, total)
		sum += total.AmountMinor
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].AmountMinor > filtered[j].AmountMinor })

	const centerX, outerR, innerR = 50.0, 26.0, 13.0
	top := pdf.GetY()
	centerY := top + outerR
	if sum > 0 {
		startAngle := -90.0 // 12 o'clock, matching the app's own donut chart
		for _, total := range filtered {
			sweep := float64(total.AmountMinor) / float64(sum) * 360
			drawDonutWedge(pdf, centerX, centerY, outerR, innerR, startAngle, startAngle+sweep, categoryColor(total.Category, colors))
			startAngle += sweep
		}
	}

	items := make([]legendItem, len(filtered))
	for i, total := range filtered {
		items[i] = legendItem{
			label: fmt.Sprintf("%s (%d)", total.Category, total.Count),
			color: categoryColor(total.Category, colors),
			value: currency + " " + formatAmount(total.AmountMinor),
		}
	}
	legendBottom := drawLegend(pdf, tr, 95, top+4, 90, items)

	bottom := centerY + outerR
	if legendBottom > bottom {
		bottom = legendBottom
	}
	pdf.SetY(bottom + 4)
	if len(others) > 0 {
		pdf.SetX(15)
		pdf.SetFont("Arial", "I", 8)
		setText(pdf, colorInkMuted)
		pdf.CellFormat(0, 5, tr(fmt.Sprintf("Also spent in %s; charts show %s only.", strings.Join(others, ", "), currency)), "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)
}

// timeBucket is one bar's worth of stacked spend, keyed by category.
type timeBucket struct {
	label   string
	amounts map[string]int64
}

// bucketBoundaries returns the start of each bucket in [start, end), at a
// granularity chosen from kind: a week report buckets by day, a month
// report by week (7-day windows anchored at start), and a year report by
// calendar month — variable-length, so it walks real month boundaries
// rather than a fixed day count.
func bucketBoundaries(kind string, start, end time.Time) []time.Time {
	var bounds []time.Time
	switch kind {
	case "week":
		for t := start; t.Before(end); t = t.AddDate(0, 0, 1) {
			bounds = append(bounds, t)
		}
	case "year":
		for t := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); t.Before(end); t = t.AddDate(0, 1, 0) {
			bounds = append(bounds, t)
		}
	default: // "month", and anything else this fell through to
		for t := start; t.Before(end); t = t.AddDate(0, 0, 7) {
			bounds = append(bounds, t)
		}
	}
	if len(bounds) == 0 {
		bounds = append(bounds, start)
	}
	return bounds
}

func bucketLabel(kind string, t time.Time) string {
	switch kind {
	case "week":
		return t.Format("Mon")
	case "year":
		return t.Format("Jan")
	default:
		return t.Format("Jan 2")
	}
}

// buildTimeBuckets sums rows into the buckets bucketBoundaries defines,
// keeping only the given currency — the same dominant-currency choice the
// pie chart makes, so both charts in one report always agree on what
// they're showing.
func buildTimeBuckets(kind string, start, end time.Time, rows []ExpenseRow, currency string) []timeBucket {
	bounds := bucketBoundaries(kind, start, end)
	buckets := make([]timeBucket, len(bounds))
	for i, b := range bounds {
		buckets[i] = timeBucket{label: bucketLabel(kind, b), amounts: map[string]int64{}}
	}
	for _, row := range rows {
		if row.Currency != currency {
			continue
		}
		idx := sort.Search(len(bounds), func(i int) bool { return bounds[i].After(row.OccurredAt) }) - 1
		if idx < 0 {
			idx = 0
		} else if idx >= len(buckets) {
			idx = len(buckets) - 1
		}
		buckets[idx].amounts[row.Category] += row.AmountMinor
	}
	return buckets
}

// niceCeiling rounds max up to a visually clean axis bound: the smallest
// value of the form {1,2,5} * 10^n at or above max, so gridline labels read
// like 50/100/150/200 rather than an arbitrary fraction of the true maximum.
func niceCeiling(max int64) int64 {
	if max <= 0 {
		return 1
	}
	magnitude := int64(1)
	for magnitude*10 <= max {
		magnitude *= 10
	}
	for _, step := range []int64{1, 2, 5, 10} {
		if candidate := step * magnitude; candidate >= max {
			return candidate
		}
	}
	return magnitude * 10
}

// writeBarChart draws a stacked bar per time bucket, segments colored by
// category. Currency and category colors match the pie chart above, so both
// charts in one report — and the totals table below them — always agree
// with each other. A no-op when there is nothing to chart.
func writeBarChart(pdf *fpdf.Fpdf, tr func(string) string, data Data, colors map[string]string) {
	if len(data.Rows) == 0 || len(data.Totals) == 0 {
		return
	}
	currency, _ := dominantCurrency(data.Totals)
	buckets := buildTimeBuckets(data.PeriodKind, data.PeriodStart, data.PeriodEnd, data.Rows, currency)

	totalByCategory := map[string]int64{}
	var maxTotal int64
	for _, b := range buckets {
		var sum int64
		for category, amount := range b.amounts {
			totalByCategory[category] += amount
			sum += amount
		}
		if sum > maxTotal {
			maxTotal = sum
		}
	}
	if maxTotal <= 0 {
		return
	}

	ensureSpace(pdf, 90)
	sectionHeading(pdf, "Spending over time")

	// Categories in one fixed order — largest total first — everywhere in
	// this chart, so a segment's stacking position never shuffles bar to
	// bar, and the legend lists the same order the stack is built in.
	categories := make([]string, 0, len(totalByCategory))
	for category := range totalByCategory {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool { return totalByCategory[categories[i]] > totalByCategory[categories[j]] })

	niceMax := niceCeiling(maxTotal)
	const plotLeft, plotWidth, plotHeight = 35.0, 150.0, 50.0
	top := pdf.GetY()
	baseY := top + plotHeight

	pdf.SetFont("Arial", "", 7)
	setText(pdf, colorInkMuted)
	setDraw(pdf, colorHairline)
	pdf.SetLineWidth(0.2)
	const ySteps = 4
	for i := 0; i <= ySteps; i++ {
		y := baseY - plotHeight*float64(i)/ySteps
		value := niceMax * int64(i) / ySteps
		pdf.Line(plotLeft, y, plotLeft+plotWidth, y)
		pdf.SetXY(15, y-2.5)
		pdf.CellFormat(plotLeft-17, 5, formatAmount(value), "", 0, "R", false, 0, "")
	}

	slotWidth := plotWidth / float64(len(buckets))
	barGap := slotWidth * 0.15
	barWidth := slotWidth - barGap
	for i, bucket := range buckets {
		x := plotLeft + float64(i)*slotWidth + barGap/2
		y := baseY
		for _, category := range categories {
			amount := bucket.amounts[category]
			if amount <= 0 {
				continue
			}
			h := float64(amount) / float64(niceMax) * plotHeight
			setFill(pdf, categoryColor(category, colors))
			pdf.Rect(x, y-h, barWidth, h, "F")
			y -= h
		}
		setText(pdf, colorInkMuted)
		pdf.SetXY(x-barGap/2, baseY+1.5)
		pdf.CellFormat(slotWidth, 4, tr(bucket.label), "", 0, "C", false, 0, "")
	}

	setDraw(pdf, colorInk)
	pdf.SetLineWidth(0.3)
	pdf.Line(plotLeft, baseY, plotLeft+plotWidth, baseY)

	items := make([]legendItem, len(categories))
	for i, category := range categories {
		items[i] = legendItem{label: category, color: categoryColor(category, colors), value: currency + " " + formatAmount(totalByCategory[category])}
	}
	bottom := drawLegend(pdf, tr, 15, baseY+8, 170, items)
	pdf.SetY(bottom + 4)
}

func writeFooter(pdf *fpdf.Fpdf, tr func(string) string) {
	pdf.SetY(-15)
	pdf.SetFont("Arial", "", 8)
	setText(pdf, colorInkMuted)
	pdf.CellFormat(0, 10, tr(fmt.Sprintf("BillPiggy · Page %d", pdf.PageNo())), "", 0, "C", false, 0, "")
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

// iconBase64 is the same "savings" icon the app itself uses next to the
// BillPiggy wordmark (Material Symbols Outlined, coral #f58a7a, matching
// Layout.tsx's sidebar mark), rasterized to a transparent PNG since fpdf
// can't render icon fonts. Embedded as a constant so the report renderer
// has no external asset/file dependency to ship or go:embed.
const iconBase64 = "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"
