package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"strings"
	"time"
)

// BudgetService coordinates budget commands and their events.
type BudgetService struct {
	repository outbound.BudgetRepository
	events     outbound.EventStore
	now        func() time.Time
}

// NewBudgetService creates a budget service.
func NewBudgetService(repository outbound.BudgetRepository, events outbound.EventStore) (*BudgetService, error) {
	if repository == nil || events == nil {
		return nil, errors.New("budget repository and event store are required")
	}
	return &BudgetService{repository: repository, events: events, now: time.Now}, nil
}

// CreateBudget creates an owner-scoped budget.
func (s *BudgetService) CreateBudget(ctx context.Context, ownerID string, budget domain.BudgetRecord) (domain.BudgetRecord, error) {
	budget.ID = uuid.NewString()
	budget.OwnerID = ownerID
	budget.Name = strings.TrimSpace(budget.Name)
	budget.Currency = strings.ToUpper(budget.Currency)
	budget.CreatedAt = s.now()
	budget.UpdatedAt = budget.CreatedAt
	if err := validateBudget(budget); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.events.Append(ctx, outbound.DomainEvent{ID: uuid.NewString(), AggregateType: "budget", AggregateID: budget.ID, EventType: "budget_created", Payload: domain.BudgetCreated{Budget: budget}, OccurredAt: budget.CreatedAt.UnixMilli(), ActorID: ownerID}); err != nil {
		return domain.BudgetRecord{}, err
	}
	return budget, s.repository.CreateBudget(ctx, budget)
}

// ListBudgets lists budgets owned by the current user.
func (s *BudgetService) ListBudgets(ctx context.Context, ownerID string) ([]domain.BudgetRecord, error) {
	return s.repository.ListBudgets(ctx, ownerID)
}
func validateBudget(b domain.BudgetRecord) error {
	if b.OwnerID == "" || b.Name == "" || b.CategoryID == "" || b.AmountLimitMinor <= 0 || len(b.Currency) != 3 || b.ThresholdPercent < 1 || b.ThresholdPercent > 100 {
		return errors.New("invalid budget")
	}
	switch b.Period {
	case domain.BudgetDaily, domain.BudgetWeekly, domain.BudgetMonthly, domain.BudgetYearly, domain.BudgetCustom:
		return nil
	}
	return errors.New("invalid budget period")
}
