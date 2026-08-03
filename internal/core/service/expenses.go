package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

var ErrInvalidExpense = errors.New("invalid expense")

// ExpenseService coordinates event creation and the expense projection.
type ExpenseService struct {
	repository outbound.ExpenseRepository
	events     outbound.EventStore
	now        func() time.Time
}

// NewExpenseService creates a service with mandatory persistence ports.
func NewExpenseService(repository outbound.ExpenseRepository, events outbound.EventStore) (*ExpenseService, error) {
	if repository == nil || events == nil {
		return nil, errors.New("expense repository and event store are required")
	}
	return &ExpenseService{repository: repository, events: events, now: time.Now}, nil
}

// CreateExpense creates an expense for the authenticated owner.
func (s *ExpenseService) CreateExpense(ctx context.Context, ownerID string, command CreateExpenseCommand) (domain.ExpenseRecord, error) {
	now := s.now()
	expense := domain.ExpenseRecord{
		ID: uuid.NewString(), OwnerID: ownerID, Title: strings.TrimSpace(command.Title), AmountMinor: command.AmountMinor,
		Currency: strings.ToUpper(strings.TrimSpace(command.Currency)), OccurredAt: command.OccurredAt.UTC(), CategoryID: command.CategoryID,
		CategoryName: strings.TrimSpace(command.CategoryName), TagIDs: append([]string(nil), command.TagIDs...), Status: command.Status,
		SharedGroupID: command.SharedGroupID, Items: append([]domain.ExpenseItem(nil), command.Items...), Latitude: command.Latitude,
		Longitude: command.Longitude, Address: strings.TrimSpace(command.Address), ReceiptObjectKey: command.ReceiptObjectKey, CreatedAt: now, UpdatedAt: now,
	}
	if expense.Status == "" {
		expense.Status = domain.ExpenseConfirmed
	}
	if err := validateExpense(expense); err != nil {
		return domain.ExpenseRecord{}, err
	}
	if err := s.events.Append(ctx, newExpenseEvent("expense_added", expense.ID, ownerID, domain.ExpenseAdded{Expense: expense}, now)); err != nil {
		return domain.ExpenseRecord{}, fmt.Errorf("append expense_added: %w", err)
	}
	if err := s.repository.CreateExpense(ctx, expense); err != nil {
		return domain.ExpenseRecord{}, fmt.Errorf("create expense projection: %w", err)
	}
	return expense, nil
}

// UpdateExpense replaces editable fields of an existing owner-scoped expense.
func (s *ExpenseService) UpdateExpense(ctx context.Context, ownerID, expenseID string, command UpdateExpenseCommand) (domain.ExpenseRecord, error) {
	expense, err := s.repository.GetExpense(ctx, ownerID, expenseID)
	if err != nil {
		return domain.ExpenseRecord{}, ErrNotFound
	}
	expense.Title, expense.AmountMinor, expense.Currency = strings.TrimSpace(command.Title), command.AmountMinor, strings.ToUpper(strings.TrimSpace(command.Currency))
	expense.OccurredAt, expense.CategoryID, expense.CategoryName = command.OccurredAt.UTC(), command.CategoryID, strings.TrimSpace(command.CategoryName)
	expense.TagIDs, expense.Status, expense.SharedGroupID, expense.Items = append([]string(nil), command.TagIDs...), command.Status, command.SharedGroupID, append([]domain.ExpenseItem(nil), command.Items...)
	expense.Latitude, expense.Longitude, expense.Address, expense.ReceiptObjectKey = command.Latitude, command.Longitude, strings.TrimSpace(command.Address), command.ReceiptObjectKey
	expense.UpdatedAt = s.now()
	if expense.Status == "" {
		expense.Status = domain.ExpenseConfirmed
	}
	if err := validateExpense(expense); err != nil {
		return domain.ExpenseRecord{}, err
	}
	if err := s.events.Append(ctx, newExpenseEvent("expense_updated", expense.ID, ownerID, domain.ExpenseUpdated{Expense: expense}, expense.UpdatedAt)); err != nil {
		return domain.ExpenseRecord{}, fmt.Errorf("append expense_updated: %w", err)
	}
	if err := s.repository.UpdateExpense(ctx, expense); err != nil {
		return domain.ExpenseRecord{}, fmt.Errorf("update expense projection: %w", err)
	}
	return expense, nil
}

// DeleteExpense makes an expense unavailable to normal read models and records an event.
func (s *ExpenseService) DeleteExpense(ctx context.Context, ownerID, expenseID string) error {
	if _, err := s.repository.GetExpense(ctx, ownerID, expenseID); err != nil {
		return ErrNotFound
	}
	now := s.now()
	if err := s.events.Append(ctx, newExpenseEvent("expense_removed", expenseID, ownerID, domain.ExpenseRemoved{ExpenseID: expenseID, OwnerID: ownerID, RemovedAt: now}, now)); err != nil {
		return fmt.Errorf("append expense_removed: %w", err)
	}
	if err := s.repository.DeleteExpense(ctx, ownerID, expenseID); err != nil {
		return fmt.Errorf("delete expense projection: %w", err)
	}
	return nil
}

// ListExpenses returns recent expenses using owner-scoped search and filters.
func (s *ExpenseService) ListExpenses(ctx context.Context, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error) {
	if filter.OwnerID == "" {
		return nil, ErrForbidden
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	return s.repository.ListExpenses(ctx, filter)
}

// CreateExpenseCommand holds all user-entered expense data.
type CreateExpenseCommand struct {
	Title, Currency, CategoryID, CategoryName, SharedGroupID, Address, ReceiptObjectKey string
	AmountMinor                                                                         int64
	OccurredAt                                                                          time.Time
	TagIDs                                                                              []string
	Status                                                                              domain.ExpenseStatus
	Items                                                                               []domain.ExpenseItem
	Latitude, Longitude                                                                 *float64
}

// UpdateExpenseCommand contains every editable expense field.
type UpdateExpenseCommand = CreateExpenseCommand

func validateExpense(expense domain.ExpenseRecord) error {
	if expense.OwnerID == "" || expense.Title == "" || expense.AmountMinor < 0 || len(expense.Currency) != 3 || expense.OccurredAt.IsZero() {
		return ErrInvalidExpense
	}
	switch expense.Status {
	case domain.ExpenseDraft, domain.ExpenseConfirmed, domain.ExpenseShared, domain.ExpenseReimbursed:
	default:
		return ErrInvalidExpense
	}
	if (expense.Latitude == nil) != (expense.Longitude == nil) {
		return ErrInvalidExpense
	}
	return nil
}

func newExpenseEvent(eventType, aggregateID, actorID string, payload any, occurredAt time.Time) outbound.DomainEvent {
	return outbound.DomainEvent{ID: uuid.NewString(), AggregateType: "expense", AggregateID: aggregateID, EventType: eventType, Payload: payload, OccurredAt: occurredAt.UnixMilli(), ActorID: actorID}
}
