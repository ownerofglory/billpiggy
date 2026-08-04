package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func newRetentionExpenseService(t *testing.T) (*service.ExpenseService, *memory.ObjectReferenceRepository) {
	t.Helper()
	repository := memory.NewExpenseRepository()
	events := memory.NewEventStore()
	objectRefs := memory.NewObjectReferenceRepository()
	unit := memory.NewUnitOfWork(repository, events, objectRefs)
	expenses, err := service.NewExpenseService(repository, events, unit)
	if err != nil {
		t.Fatalf("build expense service: %v", err)
	}
	return expenses.WithObjectReferences(objectRefs), objectRefs
}

func TestUpdateExpenseDoesNotWipeAnAttachedReceipt(t *testing.T) {
	t.Parallel()
	// The HTTP update DTO never carries a receipt key, so a general field edit
	// used to zero ReceiptObjectKey on every save after AttachReceipt set it.
	expenses, _ := newRetentionExpenseService(t)
	ctx := context.Background()
	created, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", AmountMinor: 25_00, Currency: "EUR", OccurredAt: time.Now().UTC(),
		CategoryID: "category-1", Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := expenses.AttachReceipt(ctx, "owner-1", created.ID, "receipts/owner-1/"+created.ID+"/a.jpg"); err != nil {
		t.Fatalf("attach receipt: %v", err)
	}

	updated, err := expenses.UpdateExpense(ctx, "owner-1", created.ID, service.UpdateExpenseCommand{
		Title: "Cinema and popcorn", AmountMinor: 36_00, Currency: "EUR", OccurredAt: created.OccurredAt,
		CategoryID: created.CategoryID, Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("update expense: %v", err)
	}
	if updated.ReceiptObjectKey == "" {
		t.Fatal("an unrelated field edit wiped the attached receipt")
	}
}

func TestAttachReceiptTracksAndOrphansThePreviousObject(t *testing.T) {
	t.Parallel()
	expenses, objectRefs := newRetentionExpenseService(t)
	ctx := context.Background()
	created, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", AmountMinor: 25_00, Currency: "EUR", OccurredAt: time.Now().UTC(),
		CategoryID: "category-1", Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	firstKey := "receipts/owner-1/" + created.ID + "/first.jpg"
	if _, err := expenses.AttachReceipt(ctx, "owner-1", created.ID, firstKey); err != nil {
		t.Fatalf("attach first receipt: %v", err)
	}
	secondKey := "receipts/owner-1/" + created.ID + "/second.jpg"
	if _, err := expenses.AttachReceipt(ctx, "owner-1", created.ID, secondKey); err != nil {
		t.Fatalf("attach second receipt: %v", err)
	}

	references := objectRefs.References()
	if len(references) != 2 {
		t.Fatalf("tracked %d references, want the first and second receipt", len(references))
	}
	states := map[string]domain.ObjectReferenceState{}
	for _, reference := range references {
		states[reference.ObjectKey] = reference.State
	}
	if states[firstKey] != domain.ObjectReferenceOrphaned {
		t.Fatalf("first receipt state = %s, want orphaned", states[firstKey])
	}
	if states[secondKey] != domain.ObjectReferenceActive {
		t.Fatalf("second receipt state = %s, want active", states[secondKey])
	}
}

func TestDeleteExpenseOrphansItsReceipt(t *testing.T) {
	t.Parallel()
	expenses, objectRefs := newRetentionExpenseService(t)
	ctx := context.Background()
	created, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", AmountMinor: 25_00, Currency: "EUR", OccurredAt: time.Now().UTC(),
		CategoryID: "category-1", Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	key := "receipts/owner-1/" + created.ID + "/a.jpg"
	if _, err := expenses.AttachReceipt(ctx, "owner-1", created.ID, key); err != nil {
		t.Fatalf("attach receipt: %v", err)
	}
	if err := expenses.DeleteExpense(ctx, "owner-1", created.ID); err != nil {
		t.Fatalf("delete expense: %v", err)
	}
	references := objectRefs.References()
	if len(references) != 1 || references[0].State != domain.ObjectReferenceOrphaned {
		t.Fatalf("references after delete = %#v, want the receipt orphaned", references)
	}
}

func TestDeleteExpenseWithoutAReceiptDoesNotFail(t *testing.T) {
	t.Parallel()
	// OrphanObjectsFor is unconditional in DeleteExpense; it must be a no-op
	// rather than an error when the expense never had a receipt.
	expenses, _ := newRetentionExpenseService(t)
	ctx := context.Background()
	created, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", AmountMinor: 25_00, Currency: "EUR", OccurredAt: time.Now().UTC(),
		CategoryID: "category-1", Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if err := expenses.DeleteExpense(ctx, "owner-1", created.ID); err != nil {
		t.Fatalf("delete expense without a receipt: %v", err)
	}
}

func TestRetentionServiceSweepsOrphansAndSkipsFailedDeletes(t *testing.T) {
	t.Parallel()
	objectRefs := memory.NewObjectReferenceRepository()
	objects := memory.NewObjectStore()
	ctx := context.Background()

	if err := objectRefs.TrackObject(ctx, domain.ObjectReference{ObjectKey: "receipts/a", OwnerID: "owner-1", ResourceType: "expense_receipt", ResourceID: "expense-1"}); err != nil {
		t.Fatalf("track: %v", err)
	}
	if err := objectRefs.OrphanObjectsFor(ctx, "expense_receipt", "expense-1", ""); err != nil {
		t.Fatalf("orphan: %v", err)
	}
	if err := objects.Put(ctx, "receipts/a", strings.NewReader("fake-jpeg-bytes"), 15, "image/jpeg"); err != nil {
		t.Fatalf("seed stored object: %v", err)
	}

	retention, err := service.NewRetentionService(objectRefs, objects)
	if err != nil {
		t.Fatalf("build retention service: %v", err)
	}
	swept, err := retention.SweepOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}
	if remaining := objectRefs.References(); len(remaining) != 0 {
		t.Fatalf("reference survived a successful sweep: %#v", remaining)
	}
	if keys := objects.Keys(); len(keys) != 0 {
		t.Fatalf("object survived a successful sweep: %#v", keys)
	}
}

// failingObjectStore fails Delete for a fixed set of keys, so a test can
// verify a sweep does not lose track of an object it could not remove.
type failingObjectStore struct {
	*memory.ObjectStore
	failKeys map[string]bool
}

func (s failingObjectStore) Delete(ctx context.Context, key string) error {
	if s.failKeys[key] {
		return context.DeadlineExceeded
	}
	return s.ObjectStore.Delete(ctx, key)
}

func TestRetentionServiceLeavesAFailedDeleteOrphanedForRetry(t *testing.T) {
	t.Parallel()
	objectRefs := memory.NewObjectReferenceRepository()
	objects := failingObjectStore{ObjectStore: memory.NewObjectStore(), failKeys: map[string]bool{"receipts/broken": true}}
	ctx := context.Background()

	if err := objectRefs.TrackObject(ctx, domain.ObjectReference{ObjectKey: "receipts/broken", OwnerID: "owner-1", ResourceType: "expense_receipt", ResourceID: "expense-1"}); err != nil {
		t.Fatalf("track: %v", err)
	}
	if err := objectRefs.OrphanObjectsFor(ctx, "expense_receipt", "expense-1", ""); err != nil {
		t.Fatalf("orphan: %v", err)
	}

	retention, err := service.NewRetentionService(objectRefs, objects)
	if err != nil {
		t.Fatalf("build retention service: %v", err)
	}
	swept, err := retention.SweepOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept != 0 {
		t.Fatalf("swept = %d, want 0 since the only delete failed", swept)
	}
	remaining, err := objectRefs.ClaimOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("claim orphans: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ObjectKey != "receipts/broken" {
		t.Fatalf("reference was forgotten despite a failed delete: %#v", remaining)
	}
}
