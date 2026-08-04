package domain_test

import (
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestLastCompletedReportPeriod(t *testing.T) {
	t.Parallel()
	// Wednesday, August 5, 2026.
	now := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		kind domain.AnalyticsPeriod
		want time.Time
	}{
		{domain.AnalyticsWeek, time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsMonth, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsYear, time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if got := domain.LastCompletedReportPeriod(test.kind, now); !got.Equal(test.want) {
			t.Errorf("LastCompletedReportPeriod(%s) = %s, want %s", test.kind, got, test.want)
		}
	}
}

func TestReportPeriodEnd(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		kind domain.AnalyticsPeriod
		want time.Time
	}{
		{domain.AnalyticsWeek, time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsMonth, time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)},
		{domain.AnalyticsYear, time.Date(2027, time.July, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if got := domain.ReportPeriodEnd(test.kind, start); !got.Equal(test.want) {
			t.Errorf("ReportPeriodEnd(%s) = %s, want %s", test.kind, got, test.want)
		}
	}
}

func TestValidReportPeriod(t *testing.T) {
	t.Parallel()
	for _, kind := range domain.ReportPeriods() {
		if !domain.ValidReportPeriod(kind) {
			t.Errorf("ValidReportPeriod(%s) = false, want true", kind)
		}
	}
	if domain.ValidReportPeriod(domain.AnalyticsDay) {
		t.Error("ValidReportPeriod(day) = true, want false: reports are only week/month/year")
	}
}
