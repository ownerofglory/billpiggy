package cached_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/cached"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestCachedTaxonomyRepositoryCachesAndInvalidatesOnWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := memory.NewTaxonomyRepository()
	repository := cached.NewTaxonomyRepository(inner, time.Minute)

	first, err := repository.ListCategories(ctx, "owner-1")
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	baseline := len(first)

	// A write directly against the underlying repository must not be
	// observed until the cache is invalidated — this proves reads are
	// actually served from cache, not just happening to match.
	if err := inner.CreateCategory(ctx, "owner-1", domain.ExpenseCategory{ID: "cat-1", Name: "Travel"}); err != nil {
		t.Fatalf("create category directly: %v", err)
	}
	stale, err := repository.ListCategories(ctx, "owner-1")
	if err != nil || len(stale) != baseline {
		t.Fatalf("expected a stale cached read of length %d, got %#v, %v", baseline, stale, err)
	}

	// A write through the cached decorator invalidates its own cache.
	if err := repository.CreateCategory(ctx, "owner-1", domain.ExpenseCategory{ID: "cat-2", Name: "Groceries"}); err != nil {
		t.Fatalf("create category via decorator: %v", err)
	}
	fresh, err := repository.ListCategories(ctx, "owner-1")
	if err != nil || len(fresh) != baseline+2 {
		t.Fatalf("expected a fresh read of length %d after invalidation, got %#v, %v", baseline+2, fresh, err)
	}
}

func TestCachedTaxonomyRepositoryTagsAreScopedPerOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := cached.NewTaxonomyRepository(memory.NewTaxonomyRepository(), time.Minute)
	if err := repository.CreateTag(ctx, "owner-1", domain.ExpenseTag{ID: "tag-1", Name: "Family"}); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	ownerOneTags, err := repository.ListTags(ctx, "owner-1")
	if err != nil || len(ownerOneTags) != 1 {
		t.Fatalf("owner-1 tags = %#v, %v", ownerOneTags, err)
	}
	ownerTwoTags, err := repository.ListTags(ctx, "owner-2")
	if err != nil || len(ownerTwoTags) != 0 {
		t.Fatalf("owner-2 tags = %#v, %v, want none", ownerTwoTags, err)
	}
}
