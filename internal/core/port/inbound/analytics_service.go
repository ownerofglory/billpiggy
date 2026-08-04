package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AnalyticsService is everything an HTTP handler needs from analytics queries.
type AnalyticsService interface {
	ListBudgetSuggestions(ctx context.Context, ownerID string) ([]domain.BudgetSuggestion, error)
	ListExpenseRollups(ctx context.Context, filter outbound.AnalyticsFilter) ([]domain.ExpenseRollup, error)
}
