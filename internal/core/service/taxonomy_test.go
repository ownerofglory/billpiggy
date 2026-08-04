package service_test

import (
	"context"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestTaxonomyServiceCategoryLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewTaxonomyRepository()
	taxonomy, err := service.NewTaxonomyService(repository)
	if err != nil {
		t.Fatalf("new taxonomy service: %v", err)
	}

	created, err := taxonomy.CreateCategory(ctx, "owner-1", "Travel", "#ff0000")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	updated, err := taxonomy.UpdateCategory(ctx, "owner-1", created.ID, "Vacation", "#00ff00")
	if err != nil {
		t.Fatalf("update category: %v", err)
	}
	if updated.Name != "Vacation" || updated.Color != "#00ff00" {
		t.Fatalf("updated category = %#v", updated)
	}
	categories, err := taxonomy.ListCategories(ctx, "owner-1")
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	found := false
	for _, category := range categories {
		if category.ID == created.ID {
			found, _ = true, category
		}
	}
	if !found {
		t.Fatal("updated category missing from list")
	}

	// owner-2 cannot edit or delete owner-1's category.
	if _, err := taxonomy.UpdateCategory(ctx, "owner-2", created.ID, "Hijacked", ""); err != service.ErrNotFound {
		t.Fatalf("cross-owner update: err = %v, want ErrNotFound", err)
	}
	if err := taxonomy.DeleteCategory(ctx, "owner-2", created.ID); err != service.ErrNotFound {
		t.Fatalf("cross-owner delete: err = %v, want ErrNotFound", err)
	}

	if err := taxonomy.DeleteCategory(ctx, "owner-1", created.ID); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	remaining, err := taxonomy.ListCategories(ctx, "owner-1")
	if err != nil {
		t.Fatalf("list categories after delete: %v", err)
	}
	for _, category := range remaining {
		if category.ID == created.ID {
			t.Fatal("deleted category still listed")
		}
	}
}

func TestTaxonomyServiceCannotEditDefaultCategory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewTaxonomyRepository()
	taxonomy, err := service.NewTaxonomyService(repository)
	if err != nil {
		t.Fatalf("new taxonomy service: %v", err)
	}
	defaults, err := taxonomy.ListCategories(ctx, "owner-1")
	if err != nil || len(defaults) == 0 {
		t.Fatalf("list categories: %#v, %v", defaults, err)
	}
	var defaultCategory domain.ExpenseCategory
	for _, category := range defaults {
		if category.IsDefault {
			defaultCategory = category
			break
		}
	}
	if defaultCategory.ID == "" {
		t.Fatal("expected at least one default category")
	}
	if _, err := taxonomy.UpdateCategory(ctx, "owner-1", defaultCategory.ID, "Hijacked", ""); err != service.ErrNotFound {
		t.Fatalf("update default category: err = %v, want ErrNotFound (no owner ever matches a default)", err)
	}
	if err := taxonomy.DeleteCategory(ctx, "owner-1", defaultCategory.ID); err != service.ErrNotFound {
		t.Fatalf("delete default category: err = %v, want ErrNotFound", err)
	}
}

func TestTaxonomyServiceTagLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewTaxonomyRepository()
	taxonomy, err := service.NewTaxonomyService(repository)
	if err != nil {
		t.Fatalf("new taxonomy service: %v", err)
	}
	created, err := taxonomy.CreateTag(ctx, "owner-1", "Family", "")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	updated, err := taxonomy.UpdateTag(ctx, "owner-1", created.ID, "Friends", "#123456")
	if err != nil {
		t.Fatalf("update tag: %v", err)
	}
	if updated.Name != "Friends" {
		t.Fatalf("updated tag = %#v", updated)
	}
	if err := taxonomy.DeleteTag(ctx, "owner-1", created.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	tags, err := taxonomy.ListTags(ctx, "owner-1")
	if err != nil || len(tags) != 0 {
		t.Fatalf("tags after delete = %#v, %v", tags, err)
	}
}
