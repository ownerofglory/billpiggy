package report

import (
	"testing"
	"time"
)

func TestParseHexColor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, hex string
		want      rgb
		wantOK    bool
	}{
		{name: "food orange", hex: "#f97316", want: rgb{249, 115, 22}, wantOK: true},
		{name: "no hash", hex: "84cc16", want: rgb{132, 204, 22}, wantOK: true},
		{name: "empty", hex: "", wantOK: false},
		{name: "too short", hex: "#abc", wantOK: false},
		{name: "not hex", hex: "#zzzzzz", wantOK: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseHexColor(test.hex)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if ok && got != test.want {
				t.Fatalf("color = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCategoryColorUsesTheProvidedSwatchWhenPresent(t *testing.T) {
	t.Parallel()
	colors := map[string]string{"Food": "#f97316"}
	got := categoryColor("Food", colors)
	want := rgb{249, 115, 22}
	if got != want {
		t.Fatalf("color = %+v, want %+v", got, want)
	}
}

func TestCategoryColorFallsBackDeterministicallyWithoutOne(t *testing.T) {
	t.Parallel()
	// Same category, no color entry, called twice: must return the same
	// fallback both times, and the same fallback the bar chart would derive
	// independently — otherwise a category's color would drift between the
	// pie and bar charts in the same report.
	first := categoryColor("Uncategorized", nil)
	second := categoryColor("Uncategorized", map[string]string{})
	if first != second {
		t.Fatalf("fallback color is not deterministic: %+v vs %+v", first, second)
	}
	// An empty-string color entry must not be treated as a real color.
	third := categoryColor("Uncategorized", map[string]string{"Uncategorized": ""})
	if third != first {
		t.Fatalf("empty color entry changed the fallback: %+v vs %+v", third, first)
	}
}

func TestDominantCurrencyPicksTheLargestTotal(t *testing.T) {
	t.Parallel()
	totals := []CategoryTotal{
		{Category: "Groceries", Currency: "EUR", AmountMinor: 4200},
		{Category: "Entertainment", Currency: "EUR", AmountMinor: 2500},
		{Category: "Transport", Currency: "USD", AmountMinor: 9000},
	}
	// EUR sums to 6700, USD to 9000 — USD dominates even though it's a
	// single row, which is exactly the point of summing rather than
	// counting occurrences.
	currency, others := dominantCurrency(totals)
	if currency != "USD" {
		t.Fatalf("dominant currency = %q, want USD", currency)
	}
	if len(others) != 1 || others[0] != "EUR" {
		t.Fatalf("others = %v, want [EUR]", others)
	}
}

func TestDominantCurrencySingleCurrencyHasNoOthers(t *testing.T) {
	t.Parallel()
	totals := []CategoryTotal{{Category: "Groceries", Currency: "EUR", AmountMinor: 100}}
	currency, others := dominantCurrency(totals)
	if currency != "EUR" || len(others) != 0 {
		t.Fatalf("currency = %q, others = %v, want EUR and none", currency, others)
	}
}

func TestNiceCeilingRoundsToACleanAxisBound(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		max, want int64
	}{
		{max: 0, want: 1},
		{max: 1, want: 1},
		{max: 45, want: 50},
		{max: 183, want: 200},
		{max: 200, want: 200},
		{max: 201, want: 500},
		{max: 999, want: 1000},
	} {
		if got := niceCeiling(test.max); got != test.want {
			t.Errorf("niceCeiling(%d) = %d, want %d", test.max, got, test.want)
		}
	}
}

func TestBucketBoundariesWeekBucketsByDay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	bounds := bucketBoundaries("week", start, end)
	if len(bounds) != 7 {
		t.Fatalf("got %d buckets, want 7 (one per day)", len(bounds))
	}
	if !bounds[0].Equal(start) {
		t.Fatalf("first bucket = %v, want %v", bounds[0], start)
	}
	if bucketLabel("week", start) != start.Format("Mon") {
		t.Fatalf("week bucket label = %q, want a weekday name", bucketLabel("week", start))
	}
}

func TestBucketBoundariesMonthBucketsBySevenDayWindow(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) // 31 days
	bounds := bucketBoundaries("month", start, end)
	// 31 days / 7-day windows = 5 buckets (4 full + 1 partial trailing one).
	if len(bounds) != 5 {
		t.Fatalf("got %d buckets, want 5", len(bounds))
	}
	if !bounds[1].Equal(start.AddDate(0, 0, 7)) {
		t.Fatalf("second bucket = %v, want start+7d", bounds[1])
	}
}

func TestBucketBoundariesYearBucketsByCalendarMonth(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bounds := bucketBoundaries("year", start, end)
	if len(bounds) != 12 {
		t.Fatalf("got %d buckets, want 12 (one per calendar month)", len(bounds))
	}
	if !bounds[1].Equal(time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("second bucket = %v, want 2025-02-01", bounds[1])
	}
	if bucketLabel("year", start) != "Jan" {
		t.Fatalf("year bucket label = %q, want a month abbreviation", bucketLabel("year", start))
	}
}

func TestBuildTimeBucketsAssignsRowsToTheCorrectBucketAndSkipsOtherCurrencies(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	rows := []ExpenseRow{
		{OccurredAt: start, Category: "Groceries", Currency: "EUR", AmountMinor: 100},
		{OccurredAt: start.AddDate(0, 0, 1).Add(3 * time.Hour), Category: "Food", Currency: "EUR", AmountMinor: 200},
		{OccurredAt: start.AddDate(0, 0, 1).Add(20 * time.Hour), Category: "Groceries", Currency: "EUR", AmountMinor: 50},
		// A row in a different currency must be excluded entirely, not
		// mixed into whatever bucket its date falls into.
		{OccurredAt: start, Category: "Transport", Currency: "USD", AmountMinor: 9000},
	}
	buckets := buildTimeBuckets("week", start, end, rows, "EUR")
	if len(buckets) != 7 {
		t.Fatalf("got %d buckets, want 7", len(buckets))
	}
	if buckets[0].amounts["Groceries"] != 100 {
		t.Fatalf("bucket[0] Groceries = %d, want 100", buckets[0].amounts["Groceries"])
	}
	if _, ok := buckets[0].amounts["Transport"]; ok {
		t.Fatal("USD row leaked into the EUR bucket")
	}
	if buckets[1].amounts["Food"] != 200 || buckets[1].amounts["Groceries"] != 50 {
		t.Fatalf("bucket[1] = %#v, want Food:200 Groceries:50 (both rows same day, different times)", buckets[1].amounts)
	}
}

func TestBuildTimeBucketsClampsARowAtOrAfterTheLastBoundary(t *testing.T) {
	t.Parallel()
	// A row that lands in the final (possibly partial) bucket must still be
	// counted, not dropped for having no exact-matching upper bound.
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []ExpenseRow{{OccurredAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC), Category: "Food", Currency: "EUR", AmountMinor: 500}}
	buckets := buildTimeBuckets("month", start, end, rows, "EUR")
	last := buckets[len(buckets)-1]
	if last.amounts["Food"] != 500 {
		t.Fatalf("last bucket Food = %d, want 500 (row on the final day must not be dropped)", last.amounts["Food"])
	}
}
