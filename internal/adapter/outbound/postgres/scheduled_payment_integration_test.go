//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

func newScheduledPayment(owner string, start time.Time) domain.ScheduledPayment {
	return domain.ScheduledPayment{
		ID: uuid.NewString(), OwnerID: owner, Title: "Rent", AmountMinor: 120000, Currency: "EUR",
		Frequency: domain.PaymentMonthly, StartDate: start, NextDueAt: start, AutoPost: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}

func TestScheduledPaymentRepositoryCreateListGetUpdateDelete(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	owner := seedUser(t, pool, "payments-owner@example.test")
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	payment := newScheduledPayment(owner, start)
	payment.CategoryID = defaultCategoryID(t, pool, "Home")
	payment.CategoryName = "Home"
	payment.ReminderDaysBefore = 3
	if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	listed, err := repository.ListScheduledPayments(ctx, owner, nil)
	if err != nil {
		t.Fatalf("list scheduled payments: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != payment.ID {
		t.Fatalf("listed = %#v, want the created payment", listed)
	}
	if listed[0].Title != "Rent" || listed[0].AmountMinor != 120000 || listed[0].Currency != "EUR" {
		t.Fatalf("round-tripped payment lost data: %#v", listed[0])
	}
	if listed[0].Frequency != domain.PaymentMonthly || listed[0].ReminderDaysBefore != 3 || !listed[0].AutoPost {
		t.Fatalf("round-tripped schedule lost data: %#v", listed[0])
	}
	if !listed[0].NextDueAt.Equal(start) {
		t.Fatalf("NextDueAt = %s, want %s", listed[0].NextDueAt, start)
	}

	fetched, err := repository.GetScheduledPayment(ctx, owner, payment.ID, nil)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if fetched.CategoryName != "Home" || fetched.CategoryID != payment.CategoryID {
		t.Fatalf("category not round-tripped: %#v", fetched)
	}

	fetched.AmountMinor = 130000
	fetched.UpdatedAt = time.Now().UTC()
	if err := repository.UpdateScheduledPayment(ctx, fetched); err != nil {
		t.Fatalf("update scheduled payment: %v", err)
	}
	reread, err := repository.GetScheduledPayment(ctx, owner, payment.ID, nil)
	if err != nil {
		t.Fatalf("re-read scheduled payment: %v", err)
	}
	if reread.AmountMinor != 130000 {
		t.Fatalf("AmountMinor = %d, want 130000", reread.AmountMinor)
	}

	if err := repository.DeleteScheduledPayment(ctx, owner, payment.ID); err != nil {
		t.Fatalf("delete scheduled payment: %v", err)
	}
	if _, err := repository.GetScheduledPayment(ctx, owner, payment.ID, nil); err == nil {
		t.Fatal("a soft-deleted payment must not be readable")
	}
}

// TestScheduledPaymentRepositoryRoundTripsTagsAndOptionalColumns exercises the
// uuid[] column and every nullable field, which the plain CRUD test leaves at
// their zero values.
func TestScheduledPaymentRepositoryRoundTripsTagsAndOptionalColumns(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	taxonomy := postgresadapter.NewTaxonomyRepository(pool)
	owner := seedUser(t, pool, "payments-tags@example.test")
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)

	first := domain.ExpenseTag{ID: uuid.NewString(), Name: "utilities", CreatedAt: time.Now().UTC()}
	second := domain.ExpenseTag{ID: uuid.NewString(), Name: "fixed-costs", CreatedAt: time.Now().UTC()}
	for _, tag := range []domain.ExpenseTag{first, second} {
		if err := taxonomy.CreateTag(ctx, owner, tag); err != nil {
			t.Fatalf("create tag: %v", err)
		}
	}

	payment := newScheduledPayment(owner, start)
	payment.TagIDs = []string{first.ID, second.ID}
	payment.EndDate = &end
	payment.Frequency = domain.PaymentCustom
	payment.CustomIntervalDays = 14
	if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	fetched, err := repository.GetScheduledPayment(ctx, owner, payment.ID, nil)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if len(fetched.TagIDs) != 2 {
		t.Fatalf("TagIDs = %#v, want both tags", fetched.TagIDs)
	}
	if fetched.EndDate == nil || !fetched.EndDate.Equal(end) {
		t.Fatalf("EndDate = %v, want %s", fetched.EndDate, end)
	}
	if fetched.CustomIntervalDays != 14 || fetched.Frequency != domain.PaymentCustom {
		t.Fatalf("custom schedule not round-tripped: %#v", fetched)
	}
	// LastPostedAt is null until an occurrence runs; it must scan as nil
	// rather than failing or producing a zero time.
	if fetched.LastPostedAt != nil {
		t.Fatalf("LastPostedAt = %v, want nil before any occurrence", fetched.LastPostedAt)
	}
}

// TestScheduledPaymentRepositoryClaimPostingIsExactlyOnce is the cross-replica
// guarantee: the second claim of the same occurrence must lose, so a payment
// can never post twice.
func TestScheduledPaymentRepositoryClaimPostingIsExactlyOnce(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	owner := seedUser(t, pool, "payments-claim@example.test")
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	payment := newScheduledPayment(owner, start)
	if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	posting := domain.ScheduledPaymentPosting{ScheduledPaymentID: payment.ID, DueAt: start, Kind: domain.PostingDue, PostedAt: time.Now().UTC()}
	if err := repository.ClaimPosting(ctx, posting); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := repository.ClaimPosting(ctx, posting); !errors.Is(err, outbound.ErrPostingExists) {
		t.Fatalf("second claim error = %v, want ErrPostingExists", err)
	}
	// The reminder for the same occurrence is a different row and must still
	// be claimable.
	posting.Kind = domain.PostingReminder
	if err := repository.ClaimPosting(ctx, posting); err != nil {
		t.Fatalf("reminder claim: %v", err)
	}
}

func TestScheduledPaymentRepositoryListDueRespectsPausedAndHorizon(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	owner := seedUser(t, pool, "payments-due@example.test")
	ctx := context.Background()
	now := time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)

	due := newScheduledPayment(owner, now.AddDate(0, 0, -1))
	future := newScheduledPayment(owner, now.AddDate(0, 1, 0))
	paused := newScheduledPayment(owner, now.AddDate(0, 0, -1))
	paused.Paused = true
	deleted := newScheduledPayment(owner, now.AddDate(0, 0, -1))
	for _, payment := range []domain.ScheduledPayment{due, future, paused, deleted} {
		if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
			t.Fatalf("create scheduled payment: %v", err)
		}
	}
	if err := repository.DeleteScheduledPayment(ctx, owner, deleted.ID); err != nil {
		t.Fatalf("delete scheduled payment: %v", err)
	}

	values, err := repository.ListDueScheduledPayments(ctx, now, 100)
	if err != nil {
		t.Fatalf("list due scheduled payments: %v", err)
	}
	if len(values) != 1 || values[0].ID != due.ID {
		t.Fatalf("due = %#v, want only the one live overdue payment", values)
	}
}

func TestScheduledPaymentRepositoryAdvanceSchedule(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	owner := seedUser(t, pool, "payments-advance@example.test")
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	payment := newScheduledPayment(owner, start)
	if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	next := start.AddDate(0, 1, 0)
	if err := repository.AdvanceSchedule(ctx, payment.ID, next, start, false); err != nil {
		t.Fatalf("advance schedule: %v", err)
	}
	fetched, err := repository.GetScheduledPayment(ctx, owner, payment.ID, nil)
	if err != nil {
		t.Fatalf("get scheduled payment: %v", err)
	}
	if !fetched.NextDueAt.Equal(next) {
		t.Fatalf("NextDueAt = %s, want %s", fetched.NextDueAt, next)
	}
	if fetched.LastPostedAt == nil || !fetched.LastPostedAt.Equal(start) {
		t.Fatalf("LastPostedAt = %v, want %s", fetched.LastPostedAt, start)
	}
	if fetched.Paused {
		t.Fatal("payment should not be paused")
	}

	if err := repository.AdvanceSchedule(ctx, payment.ID, next.AddDate(0, 1, 0), next, true); err != nil {
		t.Fatalf("advance and pause: %v", err)
	}
	if fetched, err = repository.GetScheduledPayment(ctx, owner, payment.ID, nil); err != nil {
		t.Fatalf("re-read scheduled payment: %v", err)
	}
	if !fetched.Paused {
		t.Fatal("payment should be paused after its final occurrence")
	}
}

// TestScheduledPaymentRepositoryIsOwnerScoped confirms the SQL, not just the
// service, refuses to hand one user another's recurring payment.
func TestScheduledPaymentRepositoryIsOwnerScoped(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewScheduledPaymentRepository(pool)
	owner := seedUser(t, pool, "payments-scope-owner@example.test")
	outsider := seedUser(t, pool, "payments-scope-outsider@example.test")
	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	payment := newScheduledPayment(owner, start)
	if err := repository.CreateScheduledPayment(ctx, payment); err != nil {
		t.Fatalf("create scheduled payment: %v", err)
	}

	if _, err := repository.GetScheduledPayment(ctx, outsider, payment.ID, nil); err == nil {
		t.Fatal("an outsider must not read another user's scheduled payment")
	}
	values, err := repository.ListScheduledPayments(ctx, outsider, nil)
	if err != nil {
		t.Fatalf("list scheduled payments: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("outsider listed %d payments, want 0", len(values))
	}
	if err := repository.DeleteScheduledPayment(ctx, outsider, payment.ID); err == nil {
		t.Fatal("an outsider must not delete another user's scheduled payment")
	}
}
