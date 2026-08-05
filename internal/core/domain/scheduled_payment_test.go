package domain_test

import (
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 9, 0, 0, 0, time.UTC)
}

// TestNextPaymentDueWalksMonthEndsWithoutDrifting is the case time.AddDate
// gets wrong: a payment anchored on the 31st must visit each month's real
// last day and then return to the 31st, never sliding forward into the
// following month and never getting stuck on the 28th.
func TestNextPaymentDueWalksMonthEndsWithoutDrifting(t *testing.T) {
	anchor := 31
	current := date(2026, time.January, 31)
	want := []time.Time{
		date(2026, time.February, 28),
		date(2026, time.March, 31),
		date(2026, time.April, 30),
		date(2026, time.May, 31),
		date(2026, time.June, 30),
	}
	for _, expected := range want {
		current = domain.NextPaymentDue(domain.PaymentMonthly, 0, anchor, current)
		if !current.Equal(expected) {
			t.Fatalf("next occurrence = %s, want %s", current.Format(time.RFC3339), expected.Format(time.RFC3339))
		}
	}
}

func TestNextPaymentDueClampsIntoLeapFebruary(t *testing.T) {
	got := domain.NextPaymentDue(domain.PaymentMonthly, 0, 30, date(2028, time.January, 30))
	if want := date(2028, time.February, 29); !got.Equal(want) {
		t.Fatalf("leap February = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextPaymentDueByFrequency(t *testing.T) {
	start := date(2026, time.March, 15)
	cases := []struct {
		name      string
		frequency domain.PaymentFrequency
		interval  int
		want      time.Time
	}{
		{"monthly", domain.PaymentMonthly, 0, date(2026, time.April, 15)},
		{"quarterly", domain.PaymentQuarterly, 0, date(2026, time.June, 15)},
		{"yearly", domain.PaymentYearly, 0, date(2027, time.March, 15)},
		{"custom fortnightly", domain.PaymentCustom, 14, date(2026, time.March, 29)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := domain.NextPaymentDue(testCase.frequency, testCase.interval, start.Day(), start)
			if !got.Equal(testCase.want) {
				t.Fatalf("next = %s, want %s", got.Format(time.RFC3339), testCase.want.Format(time.RFC3339))
			}
		})
	}
}

// TestNextPaymentDueRollsQuarterlyAndYearlyAcrossYearBoundary confirms the
// month arithmetic carries into the next year instead of producing month 13.
func TestNextPaymentDueRollsQuarterlyAndYearlyAcrossYearBoundary(t *testing.T) {
	got := domain.NextPaymentDue(domain.PaymentQuarterly, 0, 15, date(2026, time.November, 15))
	if want := date(2027, time.February, 15); !got.Equal(want) {
		t.Fatalf("quarterly across year = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestNextPaymentDueKeepsFebruary29AnchorOnCommonYears pins the yearly
// behaviour of a Feb 29 anchor, which only exists every fourth year.
func TestNextPaymentDueKeepsFebruary29AnchorOnCommonYears(t *testing.T) {
	got := domain.NextPaymentDue(domain.PaymentYearly, 0, 29, date(2028, time.February, 29))
	if want := date(2029, time.February, 28); !got.Equal(want) {
		t.Fatalf("yearly from leap day = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextPaymentDuePreservesTimeOfDay(t *testing.T) {
	start := time.Date(2026, time.January, 10, 6, 45, 0, 0, time.UTC)
	got := domain.NextPaymentDue(domain.PaymentMonthly, 0, start.Day(), start)
	if got.Hour() != 6 || got.Minute() != 45 {
		t.Fatalf("time of day = %02d:%02d, want 06:45", got.Hour(), got.Minute())
	}
}

func TestNextDueForUsesStartDateAsAnchor(t *testing.T) {
	payment := domain.ScheduledPayment{
		Frequency: domain.PaymentMonthly,
		StartDate: date(2026, time.January, 31),
	}
	// Advancing from the already-clamped February date must return to the
	// 31st, which only works if the anchor comes from StartDate.
	got := domain.NextDueFor(payment, date(2026, time.February, 28))
	if want := date(2026, time.March, 31); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestValidPaymentFrequency(t *testing.T) {
	for _, frequency := range []domain.PaymentFrequency{domain.PaymentMonthly, domain.PaymentQuarterly, domain.PaymentYearly, domain.PaymentCustom} {
		if !domain.ValidPaymentFrequency(frequency) {
			t.Fatalf("%s should be valid", frequency)
		}
	}
	for _, frequency := range []domain.PaymentFrequency{"", "weekly", "daily", "MONTHLY"} {
		if domain.ValidPaymentFrequency(frequency) {
			t.Fatalf("%q should be invalid", frequency)
		}
	}
}

func TestScheduledPaymentFinished(t *testing.T) {
	end := date(2026, time.June, 30)
	payment := domain.ScheduledPayment{EndDate: &end}
	if domain.ScheduledPaymentFinished(payment, date(2026, time.June, 30)) {
		t.Fatal("an occurrence exactly on the end date should still run")
	}
	if !domain.ScheduledPaymentFinished(payment, date(2026, time.July, 1)) {
		t.Fatal("an occurrence past the end date should not run")
	}
	if domain.ScheduledPaymentFinished(domain.ScheduledPayment{}, date(2030, time.January, 1)) {
		t.Fatal("a payment without an end date never finishes")
	}
}

func TestReminderDueAt(t *testing.T) {
	due := date(2026, time.May, 10)
	if _, ok := domain.ReminderDueAt(domain.ScheduledPayment{}, due); ok {
		t.Fatal("no reminder should be due when ReminderDaysBefore is zero")
	}
	at, ok := domain.ReminderDueAt(domain.ScheduledPayment{ReminderDaysBefore: 3}, due)
	if !ok {
		t.Fatal("a reminder should be due")
	}
	if want := date(2026, time.May, 7); !at.Equal(want) {
		t.Fatalf("reminder at = %s, want %s", at.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
