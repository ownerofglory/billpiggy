package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TaxonomyService is everything an HTTP handler needs from category and tag commands and queries.
type TaxonomyService interface {
	ListCategories(ctx context.Context, owner string) ([]domain.ExpenseCategory, error)
	CreateCategory(ctx context.Context, owner, name, color string) (domain.ExpenseCategory, error)
	UpdateCategory(ctx context.Context, owner, categoryID, name, color string) (domain.ExpenseCategory, error)
	DeleteCategory(ctx context.Context, owner, categoryID string) error
	ListTags(ctx context.Context, owner string) ([]domain.ExpenseTag, error)
	CreateTag(ctx context.Context, owner, name, color string) (domain.ExpenseTag, error)
	UpdateTag(ctx context.Context, owner, tagID, name, color string) (domain.ExpenseTag, error)
	DeleteTag(ctx context.Context, owner, tagID string) error
}
