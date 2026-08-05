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

// ErrInvalidScheduledPayment rejects a payment whose schedule or amount does
// not describe a recurrence this app can run.
var ErrInvalidScheduledPayment = errors.New("invalid scheduled payment")

// duePaymentBatchSize bounds one PostDue pass so a backlog cannot turn a
// single scheduler tick into an unbounded query.
const duePaymentBatchSize = 500

// maxReminderLeadDays bounds how far ahead ListDueScheduledPayments looks for
// payments needing an advance notice, and therefore also caps
// ReminderDaysBefore. A payment cannot ask for a reminder the scheduler would
// never see coming.
const maxReminderLeadDays = 60

// ScheduledPaymentService coordinates recurring payment commands and runs the
// occurrences they generate.
//
// It follows BudgetService for its write path — event first, then projection,
// both inside one unit of work — and ReportService for its scheduler: PostDue
// takes now explicitly so a tick and a test both pin the same instant, and one
// payment's failure never stops the rest of the batch.
type ScheduledPaymentService struct {
	repository    outbound.ScheduledPaymentRepository
	events        outbound.EventStore
	groups        outbound.GroupRepository
	unit          outbound.UnitOfWork
	expenses      outbound.ExpenseRepository
	notifications outbound.NotificationRepository
	taxonomy      outbound.TaxonomyRepository
	ids           func() string
	now           func() time.Time
}

// NewScheduledPaymentService creates a scheduled payment service.
func NewScheduledPaymentService(repository outbound.ScheduledPaymentRepository, events outbound.EventStore, groups outbound.GroupRepository, unit outbound.UnitOfWork) (*ScheduledPaymentService, error) {
	if repository == nil || events == nil || groups == nil || unit == nil {
		return nil, errors.New("scheduled payment repository, event store, group repository, and unit of work are required")
	}
	return &ScheduledPaymentService{repository: repository, events: events, groups: groups, unit: unit, ids: uuid.NewString, now: time.Now}, nil
}

// WithExpensePosting enables auto-posting: as each occurrence falls due the
// scheduler writes a confirmed expense through the same projection and event
// stream a manually entered one uses. Without it, payments only notify, and
// AutoPost has no effect.
func (s *ScheduledPaymentService) WithExpensePosting(expenses outbound.ExpenseRepository) *ScheduledPaymentService {
	s.expenses = expenses
	return s
}

// WithNotifications enables due and reminder emails. Without it, occurrences
// still post but nobody is told about them.
func (s *ScheduledPaymentService) WithNotifications(notifications outbound.NotificationRepository) *ScheduledPaymentService {
	s.notifications = notifications
	return s
}

// WithTaxonomy enables category and tag ownership validation, matching
// ExpenseService.WithTaxonomy. Without it, a payment can reference any
// category or tag ID that exists, regardless of who owns it.
func (s *ScheduledPaymentService) WithTaxonomy(taxonomy outbound.TaxonomyRepository) *ScheduledPaymentService {
	s.taxonomy = taxonomy
	return s
}

// CreateScheduledPayment schedules a new recurring payment for the owner.
func (s *ScheduledPaymentService) CreateScheduledPayment(ctx context.Context, owner domain.AppUser, payment domain.ScheduledPayment) (domain.ScheduledPayment, error) {
	now := s.now()
	payment.ID = s.ids()
	payment.OwnerID = owner.ID
	payment.Title = strings.TrimSpace(payment.Title)
	payment.Currency = strings.ToUpper(strings.TrimSpace(payment.Currency))
	payment.CategoryName = strings.TrimSpace(payment.CategoryName)
	payment.StartDate = payment.StartDate.UTC()
	payment.CreatedAt, payment.UpdatedAt = now, now
	// A schedule always starts at its own start date, including one dated in
	// the past: backdating a payment should post the occurrences that have
	// already come due, not silently skip them.
	payment.NextDueAt = payment.StartDate
	if err := validateScheduledPayment(payment); err != nil {
		return domain.ScheduledPayment{}, err
	}
	if err := s.validateSharedGroup(ctx, owner, payment.SharedGroupID); err != nil {
		return domain.ScheduledPayment{}, err
	}
	if err := s.validateOwnership(ctx, owner.ID, payment.CategoryID, payment.TagIDs); err != nil {
		return domain.ScheduledPayment{}, err
	}
	// The event is appended first so the aggregate advisory lock is taken
	// before any row lock, giving concurrent commands a consistent lock order.
	if err := s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, s.event("scheduled_payment_created", payment.ID, owner.ID, domain.ScheduledPaymentCreated{Payment: payment}, now)); err != nil {
			return fmt.Errorf("append scheduled_payment_created: %w", err)
		}
		return s.repository.CreateScheduledPayment(ctx, payment)
	}); err != nil {
		return domain.ScheduledPayment{}, err
	}
	return payment, nil
}

// ListScheduledPayments lists payments the viewer owns or that are shared
// with a group they can see.
func (s *ScheduledPaymentService) ListScheduledPayments(ctx context.Context, viewer domain.AppUser) ([]domain.ScheduledPayment, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	return s.repository.ListScheduledPayments(ctx, viewer.ID, groupIDs)
}

// GetScheduledPayment returns one payment visible to the viewer.
func (s *ScheduledPaymentService) GetScheduledPayment(ctx context.Context, viewer domain.AppUser, paymentID string) (domain.ScheduledPayment, error) {
	groupIDs, err := s.visibleGroupIDs(ctx, viewer)
	if err != nil {
		return domain.ScheduledPayment{}, err
	}
	payment, err := s.repository.GetScheduledPayment(ctx, viewer.ID, paymentID, groupIDs)
	if err != nil {
		return domain.ScheduledPayment{}, ErrNotFound
	}
	return payment, nil
}

// UpdateScheduledPayment replaces an owner-scoped payment's editable fields.
//
// Changing the recurrence re-anchors the cursor: NextDueAt is recomputed from
// the new start date so that editing a rent day actually moves the next
// charge, rather than leaving a cursor the new schedule would never produce.
// Occurrences already posted stay posted; the ledger keeps them from running
// twice.
func (s *ScheduledPaymentService) UpdateScheduledPayment(ctx context.Context, owner domain.AppUser, paymentID string, update domain.ScheduledPayment) (domain.ScheduledPayment, error) {
	payment, err := s.repository.GetScheduledPayment(ctx, owner.ID, paymentID, nil)
	if err != nil || payment.OwnerID != owner.ID {
		return domain.ScheduledPayment{}, ErrNotFound
	}
	rescheduled := !payment.StartDate.Equal(update.StartDate.UTC()) ||
		payment.Frequency != update.Frequency ||
		payment.CustomIntervalDays != update.CustomIntervalDays

	payment.Title = strings.TrimSpace(update.Title)
	payment.AmountMinor = update.AmountMinor
	payment.Currency = strings.ToUpper(strings.TrimSpace(update.Currency))
	payment.CategoryID = update.CategoryID
	payment.CategoryName = strings.TrimSpace(update.CategoryName)
	payment.TagIDs = append([]string(nil), update.TagIDs...)
	payment.SharedGroupID = update.SharedGroupID
	payment.Frequency = update.Frequency
	payment.CustomIntervalDays = update.CustomIntervalDays
	payment.StartDate = update.StartDate.UTC()
	payment.EndDate = update.EndDate
	payment.AutoPost = update.AutoPost
	payment.ReminderDaysBefore = update.ReminderDaysBefore
	payment.Paused = update.Paused
	payment.UpdatedAt = s.now()
	if rescheduled {
		payment.NextDueAt = s.rescheduleFrom(payment)
	}
	if err := validateScheduledPayment(payment); err != nil {
		return domain.ScheduledPayment{}, err
	}
	if err := s.validateSharedGroup(ctx, owner, payment.SharedGroupID); err != nil {
		return domain.ScheduledPayment{}, err
	}
	if err := s.validateOwnership(ctx, owner.ID, payment.CategoryID, payment.TagIDs); err != nil {
		return domain.ScheduledPayment{}, err
	}
	if err := s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, s.event("scheduled_payment_updated", payment.ID, owner.ID, domain.ScheduledPaymentUpdated{Payment: payment}, payment.UpdatedAt)); err != nil {
			return fmt.Errorf("append scheduled_payment_updated: %w", err)
		}
		return s.repository.UpdateScheduledPayment(ctx, payment)
	}); err != nil {
		return domain.ScheduledPayment{}, err
	}
	return payment, nil
}

// DeleteScheduledPayment soft-deletes an owner-scoped payment, stopping every
// future occurrence. Expenses it already posted are ordinary expenses and are
// deliberately left alone.
func (s *ScheduledPaymentService) DeleteScheduledPayment(ctx context.Context, owner domain.AppUser, paymentID string) error {
	payment, err := s.repository.GetScheduledPayment(ctx, owner.ID, paymentID, nil)
	if err != nil || payment.OwnerID != owner.ID {
		return ErrNotFound
	}
	now := s.now()
	return s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, s.event("scheduled_payment_removed", payment.ID, owner.ID, domain.ScheduledPaymentRemoved{ScheduledPaymentID: payment.ID, OwnerID: owner.ID, RemovedAt: now}, now)); err != nil {
			return fmt.Errorf("append scheduled_payment_removed: %w", err)
		}
		return s.repository.DeleteScheduledPayment(ctx, owner.ID, paymentID)
	})
}

// PostDue runs every occurrence that has come due as of now, and sends the
// advance notices for occurrences about to.
//
// It returns how many occurrences it posted. One payment's failure is
// collected and the pass continues, so a single bad row cannot stall every
// other user's rent.
func (s *ScheduledPaymentService) PostDue(ctx context.Context, now time.Time) (int, error) {
	horizon := now.AddDate(0, 0, maxReminderLeadDays)
	payments, err := s.repository.ListDueScheduledPayments(ctx, horizon, duePaymentBatchSize)
	if err != nil {
		return 0, fmt.Errorf("list due scheduled payments: %w", err)
	}
	var errs []error
	posted := 0
	for _, payment := range payments {
		count, err := s.runOccurrence(ctx, payment, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("scheduled payment %s: %w", payment.ID, err))
			continue
		}
		posted += count
	}
	return posted, errors.Join(errs...)
}

// runOccurrence handles the single occurrence a payment's cursor points at:
// its advance notice if that is now due, then the occurrence itself once its
// due date arrives.
func (s *ScheduledPaymentService) runOccurrence(ctx context.Context, payment domain.ScheduledPayment, now time.Time) (int, error) {
	if payment.Paused || payment.DeletedAt != nil {
		return 0, nil
	}
	if at, ok := domain.ReminderDueAt(payment, payment.NextDueAt); ok && !now.Before(at) && now.Before(payment.NextDueAt) {
		// Every tick inside the reminder window reaches this branch, so an
		// already-sent reminder is the normal case, not a failure.
		if err := s.claimAndNotify(ctx, payment, domain.PostingReminder, "", now); err != nil && !errors.Is(err, outbound.ErrPostingExists) {
			return 0, err
		}
		return 0, nil
	}
	if now.Before(payment.NextDueAt) {
		return 0, nil
	}
	// The recurrence has run past its end date: stop it rather than posting.
	if domain.ScheduledPaymentFinished(payment, payment.NextDueAt) {
		if err := s.repository.AdvanceSchedule(ctx, payment.ID, payment.NextDueAt, payment.NextDueAt, true); err != nil {
			return 0, fmt.Errorf("pause finished payment: %w", err)
		}
		return 0, nil
	}

	dueAt := payment.NextDueAt
	expenseID := ""
	if payment.AutoPost && s.expenses != nil {
		expenseID = s.ids()
	}
	// Claiming first is what makes this safe to run on every replica: the
	// loser of the race stops here without posting a duplicate expense.
	if err := s.claimAndNotify(ctx, payment, domain.PostingDue, expenseID, now); err != nil {
		if errors.Is(err, outbound.ErrPostingExists) {
			return 0, nil
		}
		return 0, err
	}
	if expenseID != "" {
		if err := s.postExpense(ctx, payment, expenseID, dueAt, now); err != nil {
			return 0, fmt.Errorf("post expense: %w", err)
		}
	}
	next := domain.NextDueFor(payment, dueAt)
	paused := domain.ScheduledPaymentFinished(payment, next)
	if err := s.repository.AdvanceSchedule(ctx, payment.ID, next, dueAt, paused); err != nil {
		return 0, fmt.Errorf("advance schedule: %w", err)
	}
	return 1, nil
}

// claimAndNotify takes the occurrence's ledger row and, only if it wins,
// queues the matching notification. ErrPostingExists is returned unwrapped so
// callers can tell "someone else has this" from a real failure.
func (s *ScheduledPaymentService) claimAndNotify(ctx context.Context, payment domain.ScheduledPayment, kind domain.PostingKind, expenseID string, now time.Time) error {
	posting := domain.ScheduledPaymentPosting{
		ScheduledPaymentID: payment.ID,
		DueAt:              payment.NextDueAt,
		Kind:               kind,
		ExpenseID:          expenseID,
		PostedAt:           now.UTC(),
	}
	if err := s.repository.ClaimPosting(ctx, posting); err != nil {
		if errors.Is(err, outbound.ErrPostingExists) {
			return err
		}
		return fmt.Errorf("claim %s posting: %w", kind, err)
	}
	return s.queueDueNotification(ctx, payment, kind, expenseID != "", now)
}

// postExpense writes the auto-posted expense through the same event stream
// and projection a manually entered expense uses, so analytics, budgets, and
// the audit trail all see it as an ordinary expense.
func (s *ScheduledPaymentService) postExpense(ctx context.Context, payment domain.ScheduledPayment, expenseID string, dueAt, now time.Time) error {
	expense := domain.ExpenseRecord{
		ID: expenseID, OwnerID: payment.OwnerID, Title: payment.Title, AmountMinor: payment.AmountMinor,
		Currency: payment.Currency, OccurredAt: dueAt.UTC(), CategoryID: payment.CategoryID,
		CategoryName: payment.CategoryName, TagIDs: append([]string(nil), payment.TagIDs...),
		Status: domain.ExpenseConfirmed, SharedGroupID: payment.SharedGroupID, CreatedAt: now, UpdatedAt: now,
	}
	if expense.SharedGroupID != "" {
		expense.Status = domain.ExpenseShared
	}
	return s.unit.Within(ctx, func(ctx context.Context) error {
		if err := s.events.Append(ctx, newExpenseEvent("expense_added", expense.ID, payment.OwnerID, domain.ExpenseAdded{Expense: expense}, now)); err != nil {
			return fmt.Errorf("append expense_added: %w", err)
		}
		if err := s.expenses.CreateExpense(ctx, expense); err != nil {
			return fmt.Errorf("create expense projection: %w", err)
		}
		if err := s.events.Append(ctx, s.event("scheduled_payment_posted", payment.ID, payment.OwnerID, domain.ScheduledPaymentPosted{
			ScheduledPaymentID: payment.ID, OwnerID: payment.OwnerID, ExpenseID: expense.ID, DueAt: dueAt.UTC(), PostedAt: now,
		}, now)); err != nil {
			return fmt.Errorf("append scheduled_payment_posted: %w", err)
		}
		return nil
	})
}

// queueDueNotification enqueues the payment's reminder or due email through
// the ordinary notification port, exactly as the budget-alert projection does.
func (s *ScheduledPaymentService) queueDueNotification(ctx context.Context, payment domain.ScheduledPayment, kind domain.PostingKind, autoPosted bool, now time.Time) error {
	if s.notifications == nil {
		return nil
	}
	delivery := domain.NotificationDelivery{
		ID:     s.ids(),
		UserID: payment.OwnerID,
		Kind:   domain.NotificationPaymentDue,
		Payload: map[string]string{
			"payment_title": payment.Title,
			"amount_minor":  fmt.Sprintf("%d", payment.AmountMinor),
			"currency":      payment.Currency,
			"due_at":        payment.NextDueAt.UTC().Format(time.RFC3339),
			"frequency":     string(payment.Frequency),
			"reminder":      boolText(kind == domain.PostingReminder),
			"auto_posted":   boolText(autoPosted),
		},
		CreatedAt: now.UTC(),
		Status:    domain.NotificationPending,
	}
	if err := s.notifications.QueueNotification(ctx, delivery); err != nil {
		return fmt.Errorf("queue payment due notification: %w", err)
	}
	return nil
}

// rescheduleFrom recomputes the cursor after an edit changed the recurrence,
// walking from the new start date to the first occurrence not already behind
// the payment's last posted one. It is bounded so a pathological start date
// far in the past cannot spin.
func (s *ScheduledPaymentService) rescheduleFrom(payment domain.ScheduledPayment) time.Time {
	next := payment.StartDate
	if payment.LastPostedAt == nil {
		return next
	}
	for i := 0; i < 1000 && !next.After(*payment.LastPostedAt); i++ {
		next = domain.NextDueFor(payment, next)
	}
	return next
}

func (s *ScheduledPaymentService) visibleGroupIDs(ctx context.Context, viewer domain.AppUser) ([]string, error) {
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

func (s *ScheduledPaymentService) validateSharedGroup(ctx context.Context, owner domain.AppUser, groupID string) error {
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

// validateOwnership mirrors ExpenseService.validateOwnership: a scheduled
// payment must not be able to reference another user's category or tag, since
// every expense it posts would inherit that reference.
func (s *ScheduledPaymentService) validateOwnership(ctx context.Context, ownerID, categoryID string, tagIDs []string) error {
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
	if len(tagIDs) == 0 {
		return nil
	}
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
	return nil
}

func (s *ScheduledPaymentService) event(eventType, aggregateID, actorID string, payload any, occurredAt time.Time) outbound.DomainEvent {
	return outbound.DomainEvent{
		ID: s.ids(), AggregateType: "scheduled_payment", AggregateID: aggregateID, EventType: eventType,
		Payload: payload, OccurredAt: occurredAt.UnixMilli(), ActorID: actorID,
	}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func validateScheduledPayment(payment domain.ScheduledPayment) error {
	if payment.OwnerID == "" || payment.Title == "" || payment.AmountMinor <= 0 || len(payment.Currency) != 3 {
		return ErrInvalidScheduledPayment
	}
	if payment.StartDate.IsZero() {
		return ErrInvalidScheduledPayment
	}
	if !domain.ValidPaymentFrequency(payment.Frequency) {
		return ErrInvalidScheduledPayment
	}
	if payment.Frequency == domain.PaymentCustom && (payment.CustomIntervalDays < 1 || payment.CustomIntervalDays > 3650) {
		return ErrInvalidScheduledPayment
	}
	if payment.ReminderDaysBefore < 0 || payment.ReminderDaysBefore > maxReminderLeadDays {
		return ErrInvalidScheduledPayment
	}
	if payment.EndDate != nil && payment.EndDate.Before(payment.StartDate) {
		return ErrInvalidScheduledPayment
	}
	return nil
}
