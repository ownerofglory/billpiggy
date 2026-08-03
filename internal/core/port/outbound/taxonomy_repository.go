package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TaxonomyRepository owns user categories and tags.
type TaxonomyRepository interface {
	ListCategories(context.Context, string) ([]domain.ExpenseCategory, error)
	CreateCategory(context.Context, string, domain.ExpenseCategory) error
	ListTags(context.Context, string) ([]domain.ExpenseTag, error)
	CreateTag(context.Context, string, domain.ExpenseTag) error
}
