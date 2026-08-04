package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TaxonomyRepository owns user categories and tags.
//
// Update and Delete are owner-scoped by construction: a default category or
// tag has no owner, so it can never match an `owner_id = $owner` condition
// and is never editable or deletable through these methods.
type TaxonomyRepository interface {
	ListCategories(context.Context, string) ([]domain.ExpenseCategory, error)
	CreateCategory(context.Context, string, domain.ExpenseCategory) error
	UpdateCategory(ctx context.Context, owner string, category domain.ExpenseCategory) error
	DeleteCategory(ctx context.Context, owner, categoryID string) error
	ListTags(context.Context, string) ([]domain.ExpenseTag, error)
	CreateTag(context.Context, string, domain.ExpenseTag) error
	UpdateTag(ctx context.Context, owner string, tag domain.ExpenseTag) error
	DeleteTag(ctx context.Context, owner, tagID string) error
}
