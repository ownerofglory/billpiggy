package cached

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/cache"
)

// TaxonomyRepository caches ListCategories and ListTags per owner: a user's
// categories and tags change rarely (an occasional create) compared to how
// often expense creation and listing read them back.
type TaxonomyRepository struct {
	inner      outbound.TaxonomyRepository
	categories *cache.Cache[string, []domain.ExpenseCategory]
	tags       *cache.Cache[string, []domain.ExpenseTag]
}

// NewTaxonomyRepository wraps inner, caching reads for ttl.
func NewTaxonomyRepository(inner outbound.TaxonomyRepository, ttl time.Duration) *TaxonomyRepository {
	return &TaxonomyRepository{
		inner: inner, categories: cache.New[string, []domain.ExpenseCategory](ttl), tags: cache.New[string, []domain.ExpenseTag](ttl),
	}
}

// ListCategories returns the cached list when present, otherwise loads and caches it.
func (r *TaxonomyRepository) ListCategories(ctx context.Context, owner string) ([]domain.ExpenseCategory, error) {
	if values, ok := r.categories.Get(owner); ok {
		return values, nil
	}
	values, err := r.inner.ListCategories(ctx, owner)
	if err != nil {
		return nil, err
	}
	r.categories.Set(owner, values)
	return values, nil
}

// CreateCategory writes through and invalidates the owner's cached list.
func (r *TaxonomyRepository) CreateCategory(ctx context.Context, owner string, value domain.ExpenseCategory) error {
	if err := r.inner.CreateCategory(ctx, owner, value); err != nil {
		return err
	}
	r.categories.Invalidate(owner)
	return nil
}

// ListTags returns the cached list when present, otherwise loads and caches it.
func (r *TaxonomyRepository) ListTags(ctx context.Context, owner string) ([]domain.ExpenseTag, error) {
	if values, ok := r.tags.Get(owner); ok {
		return values, nil
	}
	values, err := r.inner.ListTags(ctx, owner)
	if err != nil {
		return nil, err
	}
	r.tags.Set(owner, values)
	return values, nil
}

// CreateTag writes through and invalidates the owner's cached list.
func (r *TaxonomyRepository) CreateTag(ctx context.Context, owner string, value domain.ExpenseTag) error {
	if err := r.inner.CreateTag(ctx, owner, value); err != nil {
		return err
	}
	r.tags.Invalidate(owner)
	return nil
}
