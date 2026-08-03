package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// BudgetUsageProjectionName is the durable subscription name for budget usage.
const BudgetUsageProjectionName = "budget_usage"

// budgetWindow identifies one budget period to recompute.
type budgetWindow struct {
	budget domain.BudgetRecord
	start  time.Time
	end    time.Time
}

// BudgetUsageProjection maintains per-period budget spend and queues an alert
// when a budget crosses its threshold.
type BudgetUsageProjection struct {
	usage         outbound.BudgetUsageRepository
	notifications outbound.NotificationRepository
	ids           func() string
	now           func() time.Time
}

// NewBudgetUsageProjection creates the budget-usage outbox subscription.
func NewBudgetUsageProjection(usage outbound.BudgetUsageRepository, notifications outbound.NotificationRepository) (*BudgetUsageProjection, error) {
	if usage == nil || notifications == nil {
		return nil, errors.New("budget usage repository and notification repository are required")
	}
	return &BudgetUsageProjection{usage: usage, notifications: notifications, ids: uuid.NewString, now: time.Now}, nil
}

// Name returns the durable subscription name.
func (p *BudgetUsageProjection) Name() string { return BudgetUsageProjectionName }

// AggregateTypes subscribes to expense and budget events, since both change
// what a budget period has consumed.
func (p *BudgetUsageProjection) AggregateTypes() []string { return []string{"expense", "budget"} }

// Handle recomputes every budget period the event affects and queues alerts for
// thresholds it crosses.
func (p *BudgetUsageProjection) Handle(ctx context.Context, message outbox.Message) error {
	switch message.AggregateType {
	case "expense":
		return p.handleExpense(ctx, message)
	case "budget":
		return p.handleBudget(ctx, message)
	default:
		return nil
	}
}

// handleExpense mirrors the expense into the budgets context and recomputes the
// periods it moved between.
//
// Spend is recomputed from the mirror rather than adjusted by a delta. An
// incremental approach would need a second reversal ledger keyed by budget
// period, which changes shape whenever a budget's period or category is edited.
func (p *BudgetUsageProjection) handleExpense(ctx context.Context, message outbox.Message) error {
	next, applies, err := domain.DecodeExpenseContribution(message.EventType, message.Payload)
	if err != nil || !applies {
		return err
	}
	previous, found, err := p.usage.LoadContribution(ctx, next.ExpenseID)
	if err != nil {
		return err
	}
	if !next.Active {
		if !found {
			return nil
		}
		next = previous
		next.Active = false
	}
	// Save first so the sums below observe final state.
	if err := p.usage.SaveContribution(ctx, next); err != nil {
		return err
	}
	windows, err := p.affectedWindows(ctx, previous, found, next)
	if err != nil {
		return err
	}
	return p.recompute(ctx, windows, message.Replay)
}

// affectedWindows collects every budget period the change touches. An edit that
// moves an expense between categories or dates affects the periods it left as
// well as the ones it entered.
func (p *BudgetUsageProjection) affectedWindows(ctx context.Context, previous domain.ExpenseContribution, found bool, next domain.ExpenseContribution) ([]budgetWindow, error) {
	type scope struct{ ownerID, categoryID string }
	scopes := map[scope][]time.Time{}
	add := func(contribution domain.ExpenseContribution) {
		if contribution.OwnerID == "" || contribution.CategoryID == "" || contribution.OccurredAt.IsZero() {
			return
		}
		key := scope{ownerID: contribution.OwnerID, categoryID: contribution.CategoryID}
		scopes[key] = append(scopes[key], contribution.OccurredAt)
	}
	if found {
		add(previous)
	}
	add(next)

	seen := map[string]struct{}{}
	windows := make([]budgetWindow, 0, len(scopes))
	for key, times := range scopes {
		budgets, err := p.usage.ListBudgetsForCategory(ctx, key.ownerID, key.categoryID)
		if err != nil {
			return nil, err
		}
		for _, budget := range budgets {
			for _, at := range times {
				start, end := domain.BudgetWindow(budget, at)
				identity := budget.ID + "@" + strconv.FormatInt(start.UnixNano(), 10)
				if _, exists := seen[identity]; exists {
					continue
				}
				seen[identity] = struct{}{}
				windows = append(windows, budgetWindow{budget: budget, start: start, end: end})
			}
		}
	}
	return windows, nil
}

// handleBudget recomputes a budget's current period after it changes, and drops
// its usage entirely when it is removed.
func (p *BudgetUsageProjection) handleBudget(ctx context.Context, message outbox.Message) error {
	budget, applies, err := domain.DecodeBudget(message.EventType, message.Payload)
	if err != nil || !applies {
		return err
	}
	if message.EventType == "budget_removed" || budget.DeletedAt != nil {
		return p.usage.DeleteUsage(ctx, budget.ID)
	}
	// Re-read the budget so a limit, category or period change is applied from
	// the projection's own read model rather than from a possibly stale payload.
	current, found, err := p.usage.GetBudget(ctx, budget.ID)
	if err != nil {
		return err
	}
	if !found {
		return p.usage.DeleteUsage(ctx, budget.ID)
	}
	start, end := domain.BudgetWindow(current, p.now().UTC())
	return p.recompute(ctx, []budgetWindow{{budget: current, start: start, end: end}}, message.Replay)
}

// recompute totals each window from the contribution mirror, stores the usage,
// and queues an alert when a threshold rung is newly crossed.
func (p *BudgetUsageProjection) recompute(ctx context.Context, windows []budgetWindow, replay bool) error {
	for _, window := range windows {
		spent, err := p.usage.SumContributions(ctx, window.budget.OwnerID, window.budget.CategoryID, window.start, window.end)
		if err != nil {
			return err
		}
		stored, _, err := p.usage.LoadUsage(ctx, window.budget.ID, window.start)
		if err != nil {
			return err
		}
		stored.PeriodStart = window.start
		alert, crossed := domain.BudgetThresholdCrossing(window.budget, stored, spent)
		stored.SpentMinor = spent
		stored.AlertedPercent = domain.AlertedPercentFor(window.budget, stored, spent)
		stored.UpdatedAt = p.now().UTC()
		if err := p.usage.SaveUsage(ctx, stored); err != nil {
			return err
		}
		// A replayed message rebuilds state; it must not email anyone about a
		// threshold that was crossed months ago.
		if !crossed || replay {
			continue
		}
		if err := p.queueAlert(ctx, alert); err != nil {
			return err
		}
	}
	return nil
}

// queueAlert enqueues a budget-alert email.
//
// It writes through the ordinary notification port, so the row lands in the
// projector's transaction: the usage update, the outbox acknowledgement and the
// queued email all commit together or not at all.
func (p *BudgetUsageProjection) queueAlert(ctx context.Context, alert domain.BudgetAlert) error {
	payload := map[string]string{
		"budget_id":    alert.BudgetID,
		"budget_name":  alert.BudgetName,
		"currency":     alert.Currency,
		"spent_minor":  strconv.FormatInt(alert.SpentMinor, 10),
		"limit_minor":  strconv.FormatInt(alert.LimitMinor, 10),
		"percent_used": strconv.Itoa(alert.PercentUsed),
		"exceeded":     strconv.FormatBool(alert.Exceeded),
		"period_start": alert.PeriodStart.Format(time.RFC3339),
	}
	delivery := domain.NotificationDelivery{
		ID:        p.ids(),
		UserID:    alert.OwnerID,
		Kind:      domain.NotificationBudgetAlert,
		Payload:   payload,
		CreatedAt: p.now().UTC(),
		Status:    domain.NotificationPending,
	}
	if err := p.notifications.QueueNotification(ctx, delivery); err != nil {
		return fmt.Errorf("queue budget alert for %s: %w", alert.BudgetID, err)
	}
	return nil
}
