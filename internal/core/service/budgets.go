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

// BudgetService coordinates budget commands and their events.
type BudgetService struct {
	repository outbound.BudgetRepository
	events     outbound.EventStore
	groups     outbound.GroupRepository
	now        func() time.Time
}

// NewBudgetService creates a budget service.
func NewBudgetService(repository outbound.BudgetRepository, events outbound.EventStore, groups outbound.GroupRepository) (*BudgetService, error) {
	if repository == nil || events == nil || groups == nil {
		return nil, errors.New("budget repository, event store, and group repository are required")
	}
	return &BudgetService{repository: repository, events: events, groups: groups, now: time.Now}, nil
}

// CreateBudget creates an owner-scoped budget.
func (s *BudgetService) CreateBudget(ctx context.Context, owner domain.AppUser, budget domain.BudgetRecord) (domain.BudgetRecord, error) {
	budget.ID = uuid.NewString()
	budget.OwnerID = owner.ID
	budget.Name = strings.TrimSpace(budget.Name)
	budget.Currency = strings.ToUpper(budget.Currency)
	budget.CreatedAt = s.now()
	budget.UpdatedAt = budget.CreatedAt
	if err := validateBudget(budget); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.validateSharedGroup(ctx, owner, budget.SharedGroupID); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.events.Append(ctx, outbound.DomainEvent{ID: uuid.NewString(), AggregateType: "budget", AggregateID: budget.ID, EventType: "budget_created", Payload: domain.BudgetCreated{Budget: budget}, OccurredAt: budget.CreatedAt.UnixMilli(), ActorID: owner.ID}); err != nil {
		return domain.BudgetRecord{}, err
	}
	return budget, s.repository.CreateBudget(ctx, budget)
}

// ListBudgets lists budgets owned by the current user.
func (s *BudgetService) ListBudgets(ctx context.Context, viewer domain.AppUser) ([]domain.BudgetRecord, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	return s.repository.ListBudgets(ctx, viewer.ID, groupIDs)
}

// GetBudget returns a budget visible to the current user.
func (s *BudgetService) GetBudget(ctx context.Context, viewer domain.AppUser, budgetID string) (domain.BudgetRecord, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return domain.BudgetRecord{}, err
	}
	budget, err := s.repository.GetBudget(ctx, viewer.ID, budgetID, groupIDs)
	if err != nil {
		return domain.BudgetRecord{}, ErrNotFound
	}
	return budget, nil
}

// UpdateBudget replaces an owner-scoped budget and records its domain event.
func (s *BudgetService) UpdateBudget(ctx context.Context, owner domain.AppUser, budgetID string, update domain.BudgetRecord) (domain.BudgetRecord, error) {
	budget, err := s.repository.GetBudget(ctx, owner.ID, budgetID, nil)
	if err != nil || budget.OwnerID != owner.ID {
		return domain.BudgetRecord{}, ErrNotFound
	}
	budget.Name = strings.TrimSpace(update.Name)
	budget.CategoryID = update.CategoryID
	budget.AmountLimitMinor = update.AmountLimitMinor
	budget.Currency = strings.ToUpper(strings.TrimSpace(update.Currency))
	budget.ThresholdPercent = update.ThresholdPercent
	budget.Period = update.Period
	budget.DueAt = update.DueAt
	budget.SharedGroupID = update.SharedGroupID
	budget.UpdatedAt = s.now()
	if err := validateBudget(budget); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.validateSharedGroup(ctx, owner, budget.SharedGroupID); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.events.Append(ctx, outbound.DomainEvent{ID: uuid.NewString(), AggregateType: "budget", AggregateID: budget.ID, EventType: "budget_updated", Payload: domain.BudgetUpdated{Budget: budget}, OccurredAt: budget.UpdatedAt.UnixMilli(), ActorID: owner.ID}); err != nil {
		return domain.BudgetRecord{}, err
	}
	if err := s.repository.UpdateBudget(ctx, budget); err != nil {
		return domain.BudgetRecord{}, err
	}
	return budget, nil
}

// DeleteBudget soft-deletes an owner-scoped budget and records its removal event.
func (s *BudgetService) DeleteBudget(ctx context.Context, owner domain.AppUser, budgetID string) error {
	budget, err := s.repository.GetBudget(ctx, owner.ID, budgetID, nil)
	if err != nil || budget.OwnerID != owner.ID {
		return ErrNotFound
	}
	now := s.now()
	if err := s.events.Append(ctx, outbound.DomainEvent{ID: uuid.NewString(), AggregateType: "budget", AggregateID: budget.ID, EventType: "budget_removed", Payload: domain.BudgetRemoved{BudgetID: budget.ID, OwnerID: owner.ID, RemovedAt: now}, OccurredAt: now.UnixMilli(), ActorID: owner.ID}); err != nil {
		return err
	}
	return s.repository.DeleteBudget(ctx, owner.ID, budgetID)
}

func (s *BudgetService) visibleGroupIDs(ctx context.Context, viewer domain.AppUser) ([]string, error) {
	groups, err := s.groups.ListVisibleGroups(ctx, viewer.ID, viewer.Role == domain.RoleSuperAdmin)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	return ids, nil
}

func (s *BudgetService) validateSharedGroup(ctx context.Context, owner domain.AppUser, groupID string) error {
	if groupID == "" {
		return nil
	}
	groupIDs, err := s.visibleGroupIDs(ctx, owner)
	if err != nil {
		return err
	}
	for _, value := range groupIDs {
		if value == groupID {
			return nil
		}
	}
	return ErrForbidden
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
