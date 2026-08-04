//go:build integration

package postgres_test

import (
	"context"
	"testing"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestObjectReferenceRepositoryLifecycle(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewObjectReferenceRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	ctx := context.Background()

	first := domain.ObjectReference{ObjectKey: "receipts/owner/expense/first.jpg", OwnerID: owner, ResourceType: "expense_receipt", ResourceID: "expense-1"}
	if err := repository.TrackObject(ctx, first); err != nil {
		t.Fatalf("track first: %v", err)
	}
	second := domain.ObjectReference{ObjectKey: "receipts/owner/expense/second.jpg", OwnerID: owner, ResourceType: "expense_receipt", ResourceID: "expense-1"}
	if err := repository.TrackObject(ctx, second); err != nil {
		t.Fatalf("track second: %v", err)
	}

	// Replacing the receipt orphans the first key and leaves the second active.
	if err := repository.OrphanObjectsFor(ctx, "expense_receipt", "expense-1", second.ObjectKey); err != nil {
		t.Fatalf("orphan: %v", err)
	}

	orphans, err := repository.ClaimOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("claim orphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0].ObjectKey != first.ObjectKey {
		t.Fatalf("orphans = %#v, want only the first key", orphans)
	}
	if orphans[0].State != domain.ObjectReferenceOrphaned {
		t.Fatalf("orphan state = %s, want orphaned", orphans[0].State)
	}

	if err := repository.ForgetObject(ctx, first.ObjectKey); err != nil {
		t.Fatalf("forget: %v", err)
	}
	remaining, err := repository.ClaimOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("claim orphans after forget: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining orphans = %#v, want none after forgetting", remaining)
	}
}

func TestObjectReferenceRepositoryOrphanAllOnEmptyKeep(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewObjectReferenceRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	ctx := context.Background()

	for _, key := range []string{"a.jpg", "b.jpg"} {
		if err := repository.TrackObject(ctx, domain.ObjectReference{ObjectKey: "profiles/" + owner + "/" + key, OwnerID: owner, ResourceType: "user_profile_image", ResourceID: owner}); err != nil {
			t.Fatalf("track %s: %v", key, err)
		}
	}
	// A deleted resource orphans everything, which is what passing an empty
	// keep does.
	if err := repository.OrphanObjectsFor(ctx, "user_profile_image", owner, ""); err != nil {
		t.Fatalf("orphan all: %v", err)
	}
	orphans, err := repository.ClaimOrphans(ctx, 10)
	if err != nil {
		t.Fatalf("claim orphans: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %#v, want both keys orphaned", orphans)
	}
}

func TestObjectReferenceRepositoryClaimOrphansRespectsLimit(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewObjectReferenceRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		key := "receipts/" + owner + "/" + string(rune('a'+i)) + ".jpg"
		if err := repository.TrackObject(ctx, domain.ObjectReference{ObjectKey: key, OwnerID: owner, ResourceType: "expense_receipt", ResourceID: "expense-1"}); err != nil {
			t.Fatalf("track: %v", err)
		}
	}
	if err := repository.OrphanObjectsFor(ctx, "expense_receipt", "expense-1", ""); err != nil {
		t.Fatalf("orphan all: %v", err)
	}
	orphans, err := repository.ClaimOrphans(ctx, 2)
	if err != nil {
		t.Fatalf("claim orphans: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("claimed %d orphans, want the requested limit of 2", len(orphans))
	}
}

// The full sweep — including the actual object-storage delete — is covered
// against the in-memory object store in
// internal/core/service/receipt_retention_test.go. That is sufficient: the
// object-storage side of RetentionService only calls Put/Delete/Stat, whose
// PostgreSQL-adjacent behaviour is not the concern of this file. A real MinIO
// round trip belongs with the MinIO adapter's own tests instead.
