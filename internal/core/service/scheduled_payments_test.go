package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

type paymentHarness struct {
	service       *service.ScheduledPaymentService
	payments      *memory.ScheduledPaymentRepository
	expenses      *memory.ExpenseRepository
	notifications *memory.NotificationRepository
}

func newPaymentHarness(t *testing.T) paymentHarness {
	t.Helper()
	payments := memory.NewScheduledPaymentRepository()
	expenses := memory.NewExpenseRepository()
	groups := memory.NewGroupRepository()
	notifications := memory.NewNotificationRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(payments, expenses, notifications, events)
	events.WithUnitOfWork(unit)
	svc, err := service.NewScheduledPaymentService(payments, events, groups, unit)
	if err != nil {
		t.Fatalf("new scheduled payment service: %v", err)
	}
	return paymentHarness{
		service:       svc.WithExpensePosting(expenses).WithNotifications(notifications),
		payments:      payments,
		expenses:      expenses,
		notifications: notifications,
	}
}

func testOwner() domain.AppUser {
	return domain.AppUser{ID: "owner-1", Role: domain.RoleMember}
}

func rentPayment(start time.Time) domain.ScheduledPayment {
	return domain.ScheduledPayment{
		Title: "Rent", AmountMinor: 120000, Currency: "EUR", CategoryName: "Home",
		Frequency: domain.PaymentMonthly, StartDate: start, AutoPost: true,
	}
}

func (h paymentHarness) ownedExpenses(t *testing.T, ownerID string) []domain.ExpenseRecord {
	t.Helper()
	values, err := h.expenses.ListExpenses(context.Background(), outbound.ExpenseListFilter{OwnerID: ownerID, Limit: 100})
	if err != nil {
		t.Fatalf("list expenses: %v", err)
	}
	return values
}

func TestCreateScheduledPaymentStartsAtItsStartDate(t *testing.T) {
	t.Parallel()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	payment, err := harness.service.CreateScheduledPayment(context.Background(), testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}
	if !payment.NextDueAt.Equal(start) {
		t.Fatalf("NextDueAt = %s, want the start date %s", payment.NextDueAt, start)
	}
	if payment.OwnerID != "owner-1" || payment.ID == "" {
		t.Fatalf("payment not owner-scoped or missing an ID: %#v", payment)
	}
}

func TestCreateScheduledPaymentRejectsInvalidSchedules(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	earlier := start.AddDate(0, 0, -1)
	cases := map[string]func(domain.ScheduledPayment) domain.ScheduledPayment{
		"no title":            func(p domain.ScheduledPayment) domain.ScheduledPayment { p.Title = ""; return p },
		"zero amount":         func(p domain.ScheduledPayment) domain.ScheduledPayment { p.AmountMinor = 0; return p },
		"negative amount":     func(p domain.ScheduledPayment) domain.ScheduledPayment { p.AmountMinor = -1; return p },
		"bad currency":        func(p domain.ScheduledPayment) domain.ScheduledPayment { p.Currency = "EURO"; return p },
		"unknown frequency":   func(p domain.ScheduledPayment) domain.ScheduledPayment { p.Frequency = "weekly"; return p },
		"custom without days": func(p domain.ScheduledPayment) domain.ScheduledPayment { p.Frequency = domain.PaymentCustom; return p },
		"no start date":       func(p domain.ScheduledPayment) domain.ScheduledPayment { p.StartDate = time.Time{}; return p },
		"end before start":    func(p domain.ScheduledPayment) domain.ScheduledPayment { p.EndDate = &earlier; return p },
		"reminder too far": func(p domain.ScheduledPayment) domain.ScheduledPayment {
			p.ReminderDaysBefore = 400
			return p
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newPaymentHarness(t)
			if _, err := harness.service.CreateScheduledPayment(context.Background(), testOwner(), mutate(rentPayment(start))); err == nil {
				t.Fatal("expected the invalid schedule to be rejected")
			}
		})
	}
}

func TestPostDueAutoPostsAnExpenseAndAdvancesTheSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	payment, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	posted, err := harness.service.PostDue(ctx, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("post due: %v", err)
	}
	if posted != 1 {
		t.Fatalf("posted = %d, want 1", posted)
	}

	expenses := harness.ownedExpenses(t, "owner-1")
	if len(expenses) != 1 {
		t.Fatalf("expenses = %d, want 1: %#v", len(expenses), expenses)
	}
	if expenses[0].Title != "Rent" || expenses[0].AmountMinor != 120000 || expenses[0].Currency != "EUR" {
		t.Fatalf("posted expense does not match the payment: %#v", expenses[0])
	}
	if expenses[0].Status != domain.ExpenseConfirmed {
		t.Fatalf("posted expense status = %s, want confirmed so it counts toward budgets", expenses[0].Status)
	}
	if !expenses[0].OccurredAt.Equal(start) {
		t.Fatalf("OccurredAt = %s, want the due date %s", expenses[0].OccurredAt, start)
	}

	stored, err := harness.service.GetScheduledPayment(ctx, testOwner(), payment.ID)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if want := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC); !stored.NextDueAt.Equal(want) {
		t.Fatalf("NextDueAt = %s, want %s", stored.NextDueAt, want)
	}
	if stored.LastPostedAt == nil || !stored.LastPostedAt.Equal(start) {
		t.Fatalf("LastPostedAt = %v, want %s", stored.LastPostedAt, start)
	}
}

// TestPostDueIsIdempotentAcrossTicks is the property that makes the scheduler
// safe to run on every replica and to re-run after a crash: the posting ledger
// means a second pass over the same occurrence produces no second expense.
func TestPostDueIsIdempotentAcrossTicks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start)); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	posted, err := harness.service.PostDue(ctx, start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if posted != 0 {
		t.Fatalf("second pass posted = %d, want 0", posted)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 1 {
		t.Fatalf("expenses = %d, want 1 after two passes", len(expenses))
	}
}

// TestPostDueDrainsABacklogOneOccurrencePerPass pins the deliberate choice
// that each pass advances a payment by a single occurrence: a payment
// backdated by months catches up over successive ticks instead of posting a
// burst of expenses in one go.
func TestPostDueDrainsABacklogOneOccurrencePerPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.June, 1, 9, 0, 0, 0, time.UTC)
	if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start)); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	now := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	for pass := 1; pass <= 4; pass++ {
		if _, err := harness.service.PostDue(ctx, now); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	// June, July, August, September are all in the past as of September 2.
	expenses := harness.ownedExpenses(t, "owner-1")
	if len(expenses) != 4 {
		t.Fatalf("expenses = %d, want 4 (June-September): %#v", len(expenses), expenses)
	}
	// A fifth pass has nothing left to do: October is still ahead.
	posted, err := harness.service.PostDue(ctx, now)
	if err != nil {
		t.Fatalf("fifth pass: %v", err)
	}
	if posted != 0 {
		t.Fatalf("fifth pass posted = %d, want 0", posted)
	}
}

func TestPostDueSkipsPaymentsNotYetDue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start)); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	posted, err := harness.service.PostDue(ctx, start.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("post due: %v", err)
	}
	if posted != 0 {
		t.Fatalf("posted = %d, want 0 before the due date", posted)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
		t.Fatalf("expenses = %d, want 0 before the due date", len(expenses))
	}
}

func TestPostDueWithoutAutoPostOnlyNotifies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	payment := rentPayment(start)
	payment.AutoPost = false
	if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
		t.Fatalf("post due: %v", err)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
		t.Fatalf("expenses = %d, want 0 when auto-post is off", len(expenses))
	}
	queued := harness.notifications.Deliveries()
	if len(queued) != 1 || queued[0].Kind != domain.NotificationPaymentDue {
		t.Fatalf("queued notifications = %#v, want one payment_due", queued)
	}
	if queued[0].Payload["auto_posted"] != "false" {
		t.Fatalf("auto_posted = %q, want \"false\"", queued[0].Payload["auto_posted"])
	}
}

func TestPostDueSendsTheAdvanceReminderBeforeTheDueDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)
	payment := rentPayment(start)
	payment.ReminderDaysBefore = 3
	if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	// Two days out: inside the reminder window, still before the due date.
	posted, err := harness.service.PostDue(ctx, time.Date(2026, time.September, 8, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("reminder pass: %v", err)
	}
	if posted != 0 {
		t.Fatalf("posted = %d, want 0 — a reminder is not an occurrence", posted)
	}
	queued := harness.notifications.Deliveries()
	if len(queued) != 1 || queued[0].Payload["reminder"] != "true" {
		t.Fatalf("queued = %#v, want exactly one reminder notification", queued)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
		t.Fatalf("a reminder must not post an expense, got %d", len(expenses))
	}

	// A second pass inside the same window must not re-send the reminder.
	if _, err := harness.service.PostDue(ctx, time.Date(2026, time.September, 9, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("repeat reminder pass: %v", err)
	}
	if queued = harness.notifications.Deliveries(); len(queued) != 1 {
		t.Fatalf("queued = %d, want the reminder sent exactly once", len(queued))
	}

	// On the due date the occurrence itself posts.
	if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
		t.Fatalf("due pass: %v", err)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 1 {
		t.Fatalf("expenses = %d, want 1 once the payment fell due", len(expenses))
	}
}

func TestPostDueStopsAtTheEndDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)
	payment := rentPayment(start)
	payment.EndDate = &end
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), payment)
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	// September and October both fall on or before the end date; November does not.
	for _, now := range []time.Time{
		start.Add(time.Hour),
		time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC),
	} {
		if _, err := harness.service.PostDue(ctx, now); err != nil {
			t.Fatalf("post due at %s: %v", now, err)
		}
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 2 {
		t.Fatalf("expenses = %d, want 2 (September and October only)", len(expenses))
	}
	stored, err := harness.service.GetScheduledPayment(ctx, testOwner(), created.ID)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if !stored.Paused {
		t.Fatal("a payment past its end date should be paused")
	}
}

func TestPostDueIgnoresPausedAndDeletedPayments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	t.Run("paused", func(t *testing.T) {
		t.Parallel()
		harness := newPaymentHarness(t)
		payment := rentPayment(start)
		payment.Paused = true
		if _, err := harness.service.CreateScheduledPayment(ctx, testOwner(), payment); err != nil {
			t.Fatalf("create scheduled payment: %v", err)
		}
		if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
			t.Fatalf("post due: %v", err)
		}
		if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
			t.Fatalf("expenses = %d, want 0 for a paused payment", len(expenses))
		}
	})

	t.Run("deleted", func(t *testing.T) {
		t.Parallel()
		harness := newPaymentHarness(t)
		created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
		if err != nil {
			t.Fatalf("create scheduled payment: %v", err)
		}
		if err := harness.service.DeleteScheduledPayment(ctx, testOwner(), created.ID); err != nil {
			t.Fatalf("delete scheduled payment: %v", err)
		}
		if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
			t.Fatalf("post due: %v", err)
		}
		if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
			t.Fatalf("expenses = %d, want 0 for a deleted payment", len(expenses))
		}
	})
}

// TestPostDueKeepsMonthEndAnchorAcrossShortMonths is the end-to-end version of
// the domain recurrence test: a payment due on the 31st must visit February's
// last day and then return to the 31st.
func TestPostDueKeepsMonthEndAnchorAcrossShortMonths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.January, 31, 9, 0, 0, 0, time.UTC)
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	now := time.Date(2026, time.April, 5, 9, 0, 0, 0, time.UTC)
	for pass := 0; pass < 3; pass++ {
		if _, err := harness.service.PostDue(ctx, now); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	expenses := harness.ownedExpenses(t, "owner-1")
	if len(expenses) != 3 {
		t.Fatalf("expenses = %d, want 3 (Jan 31, Feb 28, Mar 31)", len(expenses))
	}
	occurred := map[string]bool{}
	for _, expense := range expenses {
		occurred[expense.OccurredAt.Format("2006-01-02")] = true
	}
	for _, want := range []string{"2026-01-31", "2026-02-28", "2026-03-31"} {
		if !occurred[want] {
			t.Fatalf("missing occurrence %s, got %v", want, occurred)
		}
	}
	stored, err := harness.service.GetScheduledPayment(ctx, testOwner(), created.ID)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if want := time.Date(2026, time.April, 30, 9, 0, 0, 0, time.UTC); !stored.NextDueAt.Equal(want) {
		t.Fatalf("NextDueAt = %s, want %s", stored.NextDueAt, want)
	}
}

func TestScheduledPaymentsAreOwnerScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}
	outsider := domain.AppUser{ID: "outsider-1", Role: domain.RoleMember}

	if _, err := harness.service.GetScheduledPayment(ctx, outsider, created.ID); err == nil {
		t.Fatal("an outsider must not read another user's scheduled payment")
	}
	if _, err := harness.service.UpdateScheduledPayment(ctx, outsider, created.ID, rentPayment(start)); err == nil {
		t.Fatal("an outsider must not edit another user's scheduled payment")
	}
	if err := harness.service.DeleteScheduledPayment(ctx, outsider, created.ID); err == nil {
		t.Fatal("an outsider must not delete another user's scheduled payment")
	}
	listed, err := harness.service.ListScheduledPayments(ctx, outsider)
	if err != nil {
		t.Fatalf("list scheduled payments: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("outsider saw %d payments, want 0", len(listed))
	}
}

// TestUpdateRescheduleMovesTheCursor covers the edit that matters most: moving
// rent from the 1st to the 15th must move the next charge, not leave a cursor
// the new schedule would never produce.
func TestUpdateRescheduleMovesTheCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	moved := rentPayment(time.Date(2026, time.September, 15, 9, 0, 0, 0, time.UTC))
	updated, err := harness.service.UpdateScheduledPayment(ctx, testOwner(), created.ID, moved)
	if err != nil {
		t.Fatalf("update scheduled payment: %v", err)
	}
	if want := time.Date(2026, time.September, 15, 9, 0, 0, 0, time.UTC); !updated.NextDueAt.Equal(want) {
		t.Fatalf("NextDueAt = %s, want %s", updated.NextDueAt, want)
	}
}

func TestUpdateWithoutReschedulingKeepsTheCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}
	if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
		t.Fatalf("post due: %v", err)
	}

	// A price rise, not a reschedule: the October cursor must survive.
	raise := rentPayment(start)
	raise.AmountMinor = 130000
	updated, err := harness.service.UpdateScheduledPayment(ctx, testOwner(), created.ID, raise)
	if err != nil {
		t.Fatalf("update scheduled payment: %v", err)
	}
	if want := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC); !updated.NextDueAt.Equal(want) {
		t.Fatalf("NextDueAt = %s, want the untouched %s", updated.NextDueAt, want)
	}
	if updated.AmountMinor != 130000 {
		t.Fatalf("AmountMinor = %d, want 130000", updated.AmountMinor)
	}
}

func TestPostDueCustomFrequencyUsesTheDayInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	payment := rentPayment(start)
	payment.Title, payment.Frequency, payment.CustomIntervalDays = "Cleaning", domain.PaymentCustom, 14
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), payment)
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	if _, err := harness.service.PostDue(ctx, start.Add(time.Hour)); err != nil {
		t.Fatalf("post due: %v", err)
	}
	stored, err := harness.service.GetScheduledPayment(ctx, testOwner(), created.ID)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if want := start.AddDate(0, 0, 14); !stored.NextDueAt.Equal(want) {
		t.Fatalf("NextDueAt = %s, want %s", stored.NextDueAt, want)
	}
}

// TestPostDueSurvivesClaimContention simulates the loser of a cross-replica
// race: the ledger row already exists, so the pass must post nothing and
// report no error rather than double-charging.
func TestPostDueSurvivesClaimContention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	harness := newPaymentHarness(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	created, err := harness.service.CreateScheduledPayment(ctx, testOwner(), rentPayment(start))
	if err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}
	// Another replica got there first.
	if err := harness.payments.ClaimPosting(ctx, domain.ScheduledPaymentPosting{
		ScheduledPaymentID: created.ID, DueAt: start, Kind: domain.PostingDue, PostedAt: start,
	}); err != nil {
		t.Fatalf("seed competing claim: %v", err)
	}

	posted, err := harness.service.PostDue(ctx, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("post due: %v", err)
	}
	if posted != 0 {
		t.Fatalf("posted = %d, want 0 when another replica claimed the occurrence", posted)
	}
	if expenses := harness.ownedExpenses(t, "owner-1"); len(expenses) != 0 {
		t.Fatalf("expenses = %d, want 0 — the occurrence belonged to another replica", len(expenses))
	}
}
