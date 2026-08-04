package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// TaxonomyService manages personal categories and tags.
type TaxonomyService struct {
	repository outbound.TaxonomyRepository
	now        func() time.Time
}

// NewTaxonomyService creates a taxonomy service.
func NewTaxonomyService(repository outbound.TaxonomyRepository) (*TaxonomyService, error) {
	if repository == nil {
		return nil, errors.New("taxonomy repository is required")
	}
	return &TaxonomyService{repository: repository, now: time.Now}, nil
}

// ListCategories lists default and personal categories.
func (s *TaxonomyService) ListCategories(ctx context.Context, owner string) ([]domain.ExpenseCategory, error) {
	return s.repository.ListCategories(ctx, owner)
}

// CreateCategory creates a personal category.
func (s *TaxonomyService) CreateCategory(ctx context.Context, owner, name, color string) (domain.ExpenseCategory, error) {
	value := domain.ExpenseCategory{ID: uuid.NewString(), Name: strings.TrimSpace(name), Color: strings.TrimSpace(color), CreatedAt: s.now()}
	if owner == "" || value.Name == "" {
		return domain.ExpenseCategory{}, errors.New("category name is required")
	}
	return value, s.repository.CreateCategory(ctx, owner, value)
}

// UpdateCategory renames or recolors a personal category. A default
// (system) category can never be updated, since it belongs to no owner.
func (s *TaxonomyService) UpdateCategory(ctx context.Context, owner, categoryID, name, color string) (domain.ExpenseCategory, error) {
	value := domain.ExpenseCategory{ID: categoryID, Name: strings.TrimSpace(name), Color: strings.TrimSpace(color)}
	if value.Name == "" {
		return domain.ExpenseCategory{}, errors.New("category name is required")
	}
	if err := s.repository.UpdateCategory(ctx, owner, value); err != nil {
		return domain.ExpenseCategory{}, ErrNotFound
	}
	return value, nil
}

// DeleteCategory removes a personal category. Fails if any expense still
// references it.
func (s *TaxonomyService) DeleteCategory(ctx context.Context, owner, categoryID string) error {
	if err := s.repository.DeleteCategory(ctx, owner, categoryID); err != nil {
		return ErrNotFound
	}
	return nil
}

// ListTags lists personal tags.
func (s *TaxonomyService) ListTags(ctx context.Context, owner string) ([]domain.ExpenseTag, error) {
	return s.repository.ListTags(ctx, owner)
}

// CreateTag creates a personal tag.
func (s *TaxonomyService) CreateTag(ctx context.Context, owner, name, color string) (domain.ExpenseTag, error) {
	value := domain.ExpenseTag{ID: uuid.NewString(), Name: strings.TrimSpace(name), Color: strings.TrimSpace(color), CreatedAt: s.now()}
	if owner == "" || value.Name == "" {
		return domain.ExpenseTag{}, errors.New("tag name is required")
	}
	return value, s.repository.CreateTag(ctx, owner, value)
}

// UpdateTag renames or recolors a personal tag.
func (s *TaxonomyService) UpdateTag(ctx context.Context, owner, tagID, name, color string) (domain.ExpenseTag, error) {
	value := domain.ExpenseTag{ID: tagID, Name: strings.TrimSpace(name), Color: strings.TrimSpace(color)}
	if value.Name == "" {
		return domain.ExpenseTag{}, errors.New("tag name is required")
	}
	if err := s.repository.UpdateTag(ctx, owner, value); err != nil {
		return domain.ExpenseTag{}, ErrNotFound
	}
	return value, nil
}

// DeleteTag removes a personal tag. Fails if any expense still carries it.
func (s *TaxonomyService) DeleteTag(ctx context.Context, owner, tagID string) error {
	if err := s.repository.DeleteTag(ctx, owner, tagID); err != nil {
		return ErrNotFound
	}
	return nil
}
