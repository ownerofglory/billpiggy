package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TaxonomyRepository stores user categories and tags for local development.
type TaxonomyRepository struct {
	mu         sync.RWMutex
	categories map[string][]domain.ExpenseCategory
	tags       map[string][]domain.ExpenseTag
}

// NewTaxonomyRepository creates a taxonomy projection with standard categories.
func NewTaxonomyRepository() *TaxonomyRepository {
	return &TaxonomyRepository{categories: map[string][]domain.ExpenseCategory{}, tags: map[string][]domain.ExpenseTag{}}
}
func (r *TaxonomyRepository) ListCategories(_ context.Context, owner string) ([]domain.ExpenseCategory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append(defaultCategories(), r.categories[owner]...), nil
}
func (r *TaxonomyRepository) CreateCategory(_ context.Context, owner string, value domain.ExpenseCategory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.categories[owner] = append(r.categories[owner], value)
	return nil
}

// UpdateCategory renames or recolors an owner-specific category. A default
// category is never in r.categories[owner], so it can never match here.
func (r *TaxonomyRepository) UpdateCategory(_ context.Context, owner string, category domain.ExpenseCategory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, value := range r.categories[owner] {
		if value.ID == category.ID {
			r.categories[owner][i].Name, r.categories[owner][i].Color = category.Name, category.Color
			return nil
		}
	}
	return errNotFound
}

// DeleteCategory removes an owner-specific category.
func (r *TaxonomyRepository) DeleteCategory(_ context.Context, owner, categoryID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, value := range r.categories[owner] {
		if value.ID == categoryID {
			r.categories[owner] = append(r.categories[owner][:i], r.categories[owner][i+1:]...)
			return nil
		}
	}
	return errNotFound
}

func (r *TaxonomyRepository) ListTags(_ context.Context, owner string) ([]domain.ExpenseTag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// []domain.ExpenseTag{} rather than (...)(nil): an owner with zero tags
	// would otherwise serialize the whole list as null instead of [].
	return append([]domain.ExpenseTag{}, r.tags[owner]...), nil
}
func (r *TaxonomyRepository) CreateTag(_ context.Context, owner string, value domain.ExpenseTag) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags[owner] = append(r.tags[owner], value)
	return nil
}

// UpdateTag renames or recolors an owner-specific tag.
func (r *TaxonomyRepository) UpdateTag(_ context.Context, owner string, tag domain.ExpenseTag) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, value := range r.tags[owner] {
		if value.ID == tag.ID {
			r.tags[owner][i].Name, r.tags[owner][i].Color = tag.Name, tag.Color
			return nil
		}
	}
	return errNotFound
}

// DeleteTag removes an owner-specific tag.
func (r *TaxonomyRepository) DeleteTag(_ context.Context, owner, tagID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, value := range r.tags[owner] {
		if value.ID == tagID {
			r.tags[owner] = append(r.tags[owner][:i], r.tags[owner][i+1:]...)
			return nil
		}
	}
	return errNotFound
}

// Snapshot copies the stored taxonomy and returns a function restoring it.
func (r *TaxonomyRepository) Snapshot() func() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	categories := make(map[string][]domain.ExpenseCategory, len(r.categories))
	for owner, values := range r.categories {
		categories[owner] = append([]domain.ExpenseCategory(nil), values...)
	}
	tags := make(map[string][]domain.ExpenseTag, len(r.tags))
	for owner, values := range r.tags {
		tags[owner] = append([]domain.ExpenseTag(nil), values...)
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.categories, r.tags = categories, tags
	}
}

func defaultCategories() []domain.ExpenseCategory { return domain.DefaultCategories() }
