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
func (r *TaxonomyRepository) ListTags(_ context.Context, owner string) ([]domain.ExpenseTag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]domain.ExpenseTag(nil), r.tags[owner]...), nil
}
func (r *TaxonomyRepository) CreateTag(_ context.Context, owner string, value domain.ExpenseTag) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags[owner] = append(r.tags[owner], value)
	return nil
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
