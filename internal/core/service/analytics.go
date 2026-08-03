package service

import (
	"context"
	"errors"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsService exposes analytics read models without coupling them to expense writes.
type AnalyticsService struct {
	repository outbound.AnalyticsRepository
	budgets    outbound.BudgetRepository
	now        func() time.Time
}

// NewAnalyticsService creates an analytics query service.
func NewAnalyticsService(repository outbound.AnalyticsRepository, budgets outbound.BudgetRepository) (*AnalyticsService, error) {
	if repository == nil || budgets == nil {
		return nil, errors.New("analytics and budget repositories are required")
	}
	return &AnalyticsService{repository: repository, budgets: budgets, now: time.Now}, nil
}

// ListBudgetSuggestions returns threshold-driven spending suggestions for the current month.
func (s *AnalyticsService) ListBudgetSuggestions(ctx context.Context, ownerID string) ([]domain.BudgetSuggestion, error) {
	if ownerID == "" {
		return nil, ErrForbidden
	}
	now := s.now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	rollups, err := s.repository.ListExpenseRollups(ctx, outbound.AnalyticsFilter{OwnerID: ownerID, Period: domain.AnalyticsMonth, From: start, To: now})
	if err != nil {
		return nil, err
	}
	spent := map[string]int64{}
	for _, rollup := range rollups {
		spent[rollup.CategoryID] += rollup.AmountMinor
	}
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return nil, err
	}
	values := []domain.BudgetSuggestion{}
	for _, budget := range budgets {
		value := spent[budget.CategoryID]
		percent := int(value * 100 / budget.AmountLimitMinor)
		if percent < budget.ThresholdPercent {
			continue
		}
		message := "You are approaching this budget."
		if percent >= 100 {
			message = "You have exceeded this budget."
		}
		values = append(values, domain.BudgetSuggestion{BudgetID: budget.ID, CategoryID: budget.CategoryID, Currency: budget.Currency, SpentMinor: value, LimitMinor: budget.AmountLimitMinor, PercentUsed: percent, Message: message})
	}
	return values, nil
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
