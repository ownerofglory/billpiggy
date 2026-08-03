package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TaxonomyRepository persists user categories and tags in PostgreSQL.
type TaxonomyRepository struct{ pool *pgxpool.Pool }

// NewTaxonomyRepository creates a PostgreSQL taxonomy adapter.
func NewTaxonomyRepository(pool *pgxpool.Pool) *TaxonomyRepository {
	return &TaxonomyRepository{pool: pool}
}

// ListCategories returns system defaults and categories created by the owner.
func (r *TaxonomyRepository) ListCategories(ctx context.Context, owner string) ([]domain.ExpenseCategory, error) {
	rows, err := r.pool.Query(ctx, `select id::text,name,coalesce(color,''),is_default,created_at from expenses.categories where is_default or owner_id=$1 order by is_default desc,name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.ExpenseCategory{}
	for rows.Next() {
		var value domain.ExpenseCategory
		if err := rows.Scan(&value.ID, &value.Name, &value.Color, &value.IsDefault, &value.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// CreateCategory stores an owner-specific category.
func (r *TaxonomyRepository) CreateCategory(ctx context.Context, owner string, value domain.ExpenseCategory) error {
	_, err := r.pool.Exec(ctx, `insert into expenses.categories(id,owner_id,name,color,is_default,created_at) values($1,$2,$3,nullif($4,''),false,$5)`, value.ID, owner, value.Name, value.Color, value.CreatedAt)
	return err
}

// ListTags returns tags created by the owner.
func (r *TaxonomyRepository) ListTags(ctx context.Context, owner string) ([]domain.ExpenseTag, error) {
	rows, err := r.pool.Query(ctx, `select id::text,name,coalesce(color,''),created_at from expenses.tags where owner_id=$1 order by name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.ExpenseTag{}
	for rows.Next() {
		var value domain.ExpenseTag
		if err := rows.Scan(&value.ID, &value.Name, &value.Color, &value.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// CreateTag stores an owner-specific tag.
func (r *TaxonomyRepository) CreateTag(ctx context.Context, owner string, value domain.ExpenseTag) error {
	_, err := r.pool.Exec(ctx, `insert into expenses.tags(id,owner_id,name,color,created_at) values($1,$2,$3,nullif($4,''),$5)`, value.ID, owner, value.Name, value.Color, value.CreatedAt)
	return err
}
