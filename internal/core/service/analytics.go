package service

import (
	"context"
	"errors"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsService exposes analytics read models without coupling them to expense writes.
type AnalyticsService struct{ repository outbound.AnalyticsRepository }

// NewAnalyticsService creates an analytics query service.
func NewAnalyticsService(repository outbound.AnalyticsRepository) (*AnalyticsService, error) {
	if repository == nil {
		return nil, errors.New("analytics repository is required")
	}
	return &AnalyticsService{repository: repository}, nil
}

// ListExpenseRollups returns category/tag rollups for the requested owner and time window.
func (s *AnalyticsService) ListExpenseRollups(ctx context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error) {
	if filter.OwnerID == "" {
		return nil, ErrForbidden
	}
	switch filter.Period {
	case domain.AnalyticsDay, domain.AnalyticsWeek, domain.AnalyticsMonth, domain.AnalyticsYear:
	default:
		return nil, errors.New("invalid analytics period")
	}
	return s.repository.ListExpenseRollups(ctx, filter)
}
