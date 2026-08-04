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

// ObjectResourceExpenseReceipt identifies expense receipts to the object
// reference tracker.
const ObjectResourceExpenseReceipt = "expense_receipt"

// ExpenseService coordinates event creation and the expense projection.
type ExpenseService struct {
	repository outbound.ExpenseRepository
	events     outbound.EventStore
	unit       outbound.UnitOfWork
	objectRefs outbound.ObjectReferenceRepository
	groups     outbound.GroupRepository
	taxonomy   outbound.TaxonomyRepository
	now        func() time.Time
}

// NewExpenseService creates a service with mandatory persistence ports.
func NewExpenseService(repository outbound.ExpenseRepository, events outbound.EventStore, unit outbound.UnitOfWork) (*ExpenseService, error) {
	if repository == nil || events == nil || unit == nil {
		return nil, errors.New("expense repository, event store, and unit of work are required")
	}
	return &ExpenseService{repository: repository, events: events, unit: unit, now: time.Now}, nil
}

// WithObjectReferences enables receipt retention tracking. Without it,
// AttachReceipt and DeleteExpense skip tracking and replaced or deleted
// receipts are never reclaimed.
func (s *ExpenseService) WithObjectReferences(references outbound.ObjectReferenceRepository) *ExpenseService {
	s.objectRefs = references
	return s
}

// WithGroups enables shared-group visibility for GetExpenseForViewer and
// ListExpensesForViewer. Without it, those methods see only the viewer's own
// expenses.
func (s *ExpenseService) WithGroups(groups outbound.GroupRepository) *ExpenseService {
	s.groups = groups
	return s
}

// WithTaxonomy enables category/tag ownership validation on create and
// update. Without it, an expense can reference any category or tag ID that
// exists, regardless of who owns it.
func (s *ExpenseService) WithTaxonomy(taxonomy outbound.TaxonomyRepository) *ExpenseService {
	s.taxonomy = taxonomy
	return s
}

// validateOwnership confirms categoryID (a default or one owned by ownerID)
// and every tag in tagIDs belong to ownerID, so an expense can never
// reference another user's private category or tag. A no-op without
// WithTaxonomy configured.
func (s *ExpenseService) validateOwnership(ctx context.Context, ownerID, categoryID string, tagIDs []string) error {
	if s.taxonomy == nil {
		return nil
	}
	if categoryID != "" {
		categories, err := s.taxonomy.ListCategories(ctx, ownerID)
		if err != nil {
			return fmt.Errorf("list categories: %w", err)
		}
		found := false
		for _, category := range categories {
			if category.ID == categoryID {
				found = true
				break
			}
		}
		if !found {
			return ErrForbidden
		}
	}
	if len(tagIDs) > 0 {
		tags, err := s.taxonomy.ListTags(ctx, ownerID)
		if err != nil {
			return fmt.Errorf("list tags: %w", err)
		}
		owned := make(map[string]bool, len(tags))
		for _, tag := range tags {
			owned[tag.ID] = true
		}
		for _, tagID := range tagIDs {
			if !owned[tagID] {
				return ErrForbidden
			}
		}
	}
	return nil
}

// visibleGroupIDs returns the groups whose shared expenses viewer may read:
// every group for a super-admin, or the groups viewer created or belongs to
// otherwise. Returns nil without WithGroups configured.
func (s *ExpenseService) visibleGroupIDs(ctx context.Context, viewer domain.AppUser) ([]string, error) {
	if s.groups == nil {
		return nil, nil
	}
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
	if err := s.validateOwnership(ctx, ownerID, expense.CategoryID, expense.TagIDs); err != nil {
		return domain.ExpenseRecord{}, err
	}
	// The event is appended first so the aggregate advisory lock is taken
	// before any row lock, giving concurrent commands a consistent lock order.
	if err := s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, newExpenseEvent("expense_added", expense.ID, ownerID, domain.ExpenseAdded{Expense: expense}, now)); err != nil {
			return fmt.Errorf("append expense_added: %w", err)
		}
		if err := s.repository.CreateExpense(ctx, expense); err != nil {
			return fmt.Errorf("create expense projection: %w", err)
		}
		return nil
	}); err != nil {
		return domain.ExpenseRecord{}, err
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
	expense.Latitude, expense.Longitude, expense.Address = command.Latitude, command.Longitude, strings.TrimSpace(command.Address)
	// ReceiptObjectKey is deliberately left untouched here: the HTTP DTO never
	// carries it, so overwriting it from the command wiped every receipt on
	// the next unrelated field edit. It is set only through AttachReceipt.
	expense.UpdatedAt = s.now()
	if expense.Status == "" {
		expense.Status = domain.ExpenseConfirmed
	}
	if err := validateExpense(expense); err != nil {
		return domain.ExpenseRecord{}, err
	}
	if err := s.validateOwnership(ctx, ownerID, expense.CategoryID, expense.TagIDs); err != nil {
		return domain.ExpenseRecord{}, err
	}
	if err := s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, newExpenseEvent("expense_updated", expense.ID, ownerID, domain.ExpenseUpdated{Expense: expense}, expense.UpdatedAt)); err != nil {
			return fmt.Errorf("append expense_updated: %w", err)
		}
		if err := s.repository.UpdateExpense(ctx, expense); err != nil {
			return fmt.Errorf("update expense projection: %w", err)
		}
		return nil
	}); err != nil {
		return domain.ExpenseRecord{}, err
	}
	return expense, nil
}

// DeleteExpense makes an expense unavailable to normal read models and records an event.
func (s *ExpenseService) DeleteExpense(ctx context.Context, ownerID, expenseID string) error {
	if _, err := s.repository.GetExpense(ctx, ownerID, expenseID); err != nil {
		return ErrNotFound
	}
	now := s.now()
	return s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, newExpenseEvent("expense_removed", expenseID, ownerID, domain.ExpenseRemoved{ExpenseID: expenseID, OwnerID: ownerID, RemovedAt: now}, now)); err != nil {
			return fmt.Errorf("append expense_removed: %w", err)
		}
		if err := s.repository.DeleteExpense(ctx, ownerID, expenseID); err != nil {
			return fmt.Errorf("delete expense projection: %w", err)
		}
		if s.objectRefs != nil {
			// Orphaning is a no-op when the expense never had a receipt; no
			// need to branch on whether one was ever attached.
			if err := s.objectRefs.OrphanObjectsFor(ctx, ObjectResourceExpenseReceipt, expenseID, ""); err != nil {
				return fmt.Errorf("orphan receipt for deleted expense: %w", err)
			}
		}
		return nil
	})
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

// GetExpense returns one expense only when it belongs to the authenticated owner.
func (s *ExpenseService) GetExpense(ctx context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error) {
	expense, err := s.repository.GetExpense(ctx, ownerID, expenseID)
	if err != nil {
		return domain.ExpenseRecord{}, ErrNotFound
	}
	return expense, nil
}

// ListExpensesForViewer returns recent expenses the viewer owns, plus
// expenses shared with any group they belong to.
func (s *ExpenseService) ListExpensesForViewer(ctx context.Context, viewer domain.AppUser, filter outbound.ExpenseListFilter) ([]domain.ExpenseRecord, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	filter.OwnerID, filter.SharedGroupIDs = viewer.ID, groupIDs
	return s.ListExpenses(ctx, filter)
}

// GetExpenseForViewer returns one expense the viewer owns or that is shared
// with a group they belong to.
func (s *ExpenseService) GetExpenseForViewer(ctx context.Context, viewer domain.AppUser, expenseID string) (domain.ExpenseRecord, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return domain.ExpenseRecord{}, err
	}
	expense, err := s.repository.GetExpenseVisible(ctx, viewer.ID, expenseID, groupIDs)
	if err != nil {
		return domain.ExpenseRecord{}, ErrNotFound
	}
	return expense, nil
}

// AttachReceipt records an uploaded receipt object for an owner-scoped
// expense. A previously attached receipt is orphaned so the retention sweeper
// reclaims it rather than leaving it in the store forever.
func (s *ExpenseService) AttachReceipt(ctx context.Context, ownerID, expenseID, objectKey string) (domain.ExpenseRecord, error) {
	expense, err := s.repository.GetExpense(ctx, ownerID, expenseID)
	if err != nil {
		return domain.ExpenseRecord{}, ErrNotFound
	}
	if strings.TrimSpace(objectKey) == "" {
		return domain.ExpenseRecord{}, ErrInvalidExpense
	}
	previousKey := expense.ReceiptObjectKey
	expense.ReceiptObjectKey, expense.UpdatedAt = objectKey, s.now()
	if err := s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, newExpenseEvent("expense_updated", expense.ID, ownerID, domain.ExpenseUpdated{Expense: expense}, expense.UpdatedAt)); err != nil {
			return err
		}
		if err := s.repository.UpdateExpense(ctx, expense); err != nil {
			return err
		}
		if s.objectRefs != nil {
			if err := s.objectRefs.TrackObject(ctx, domain.ObjectReference{
				ObjectKey: objectKey, OwnerID: ownerID, ResourceType: ObjectResourceExpenseReceipt, ResourceID: expenseID,
			}); err != nil {
				return fmt.Errorf("track receipt object: %w", err)
			}
			if previousKey != "" && previousKey != objectKey {
				if err := s.objectRefs.OrphanObjectsFor(ctx, ObjectResourceExpenseReceipt, expenseID, objectKey); err != nil {
					return fmt.Errorf("orphan previous receipt: %w", err)
				}
			}
		}
		return nil
	}); err != nil {
		return domain.ExpenseRecord{}, err
	}
	return expense, nil
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
