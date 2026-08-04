package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// TaxonomyRepository persists user categories and tags in PostgreSQL.
type TaxonomyRepository struct{ pool *pgxpool.Pool }

// NewTaxonomyRepository creates a PostgreSQL taxonomy adapter.
func NewTaxonomyRepository(pool *pgxpool.Pool) *TaxonomyRepository {
	return &TaxonomyRepository{pool: pool}
}

// ListCategories returns system defaults and categories created by the owner.
func (r *TaxonomyRepository) ListCategories(ctx context.Context, owner string) ([]domain.ExpenseCategory, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select id::text,name,coalesce(color,''),is_default,created_at from expenses.categories where is_default or owner_id=$1 order by is_default desc,name`, owner)
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
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into expenses.categories(id,owner_id,name,color,is_default,created_at) values($1,$2,$3,nullif($4,''),false,$5)`, value.ID, owner, value.Name, value.Color, value.CreatedAt)
	return err
}

// UpdateCategory renames or recolors an owner-specific category. A default
// category (owner_id is null) can never match owner_id=$1, so it can never
// be edited through this method.
func (r *TaxonomyRepository) UpdateCategory(ctx context.Context, owner string, category domain.ExpenseCategory) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update expenses.categories set name=$3,color=nullif($4,'') where id=$1 and owner_id=$2`, category.ID, owner, category.Name, category.Color)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteCategory removes an owner-specific category. Fails with a foreign-key
// violation if any expense still references it, which the service surfaces
// as a plain error rather than silently reassigning those expenses.
func (r *TaxonomyRepository) DeleteCategory(ctx context.Context, owner, categoryID string) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `delete from expenses.categories where id=$1 and owner_id=$2`, categoryID, owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListTags returns tags created by the owner.
func (r *TaxonomyRepository) ListTags(ctx context.Context, owner string) ([]domain.ExpenseTag, error) {
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `select id::text,name,coalesce(color,''),created_at from expenses.tags where owner_id=$1 order by name`, owner)
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
	_, err := pgxtx.From(ctx, r.pool).Exec(ctx, `insert into expenses.tags(id,owner_id,name,color,created_at) values($1,$2,$3,nullif($4,''),$5)`, value.ID, owner, value.Name, value.Color, value.CreatedAt)
	return err
}

// UpdateTag renames or recolors an owner-specific tag.
func (r *TaxonomyRepository) UpdateTag(ctx context.Context, owner string, tag domain.ExpenseTag) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `update expenses.tags set name=$3,color=nullif($4,'') where id=$1 and owner_id=$2`, tag.ID, owner, tag.Name, tag.Color)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteTag removes an owner-specific tag. Fails with a foreign-key violation
// if any expense still carries it.
func (r *TaxonomyRepository) DeleteTag(ctx context.Context, owner, tagID string) error {
	command, err := pgxtx.From(ctx, r.pool).Exec(ctx, `delete from expenses.tags where id=$1 and owner_id=$2`, tagID, owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}
