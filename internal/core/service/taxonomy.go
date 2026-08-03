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
