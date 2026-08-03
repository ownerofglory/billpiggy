package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// ExpenseContribution is the expense state a projection has already applied.
// Projections keep one per expense so an update can reverse the exact amounts,
// category and tags that were previously counted, even when the edit changed
// every one of those dimensions.
type ExpenseContribution struct {
	// ExpenseID identifies the expense this contribution was derived from.
	ExpenseID string
	// OwnerID is the expense owner the contribution counts against.
	OwnerID string
	// CategoryID is the category the contribution was applied to.
	CategoryID string
	// Currency is the ISO 4217 code of the applied amount.
	Currency string
	// TagIDs are the tags the contribution was applied to.
	TagIDs []string
	// AmountMinor is the applied amount in minor currency units.
	AmountMinor int64
	// OccurredAt places the contribution in its rollup periods.
	OccurredAt time.Time
	// Active reports whether the contribution is currently counted.
	Active bool
}

// RollupDelta is a signed adjustment to one analytics bucket. A reversal is the
// same delta with negated amount and count.
type RollupDelta struct {
	// OwnerID scopes the bucket to one user.
	OwnerID string
	// CategoryID names the category bucket; empty for a tag bucket.
	CategoryID string
	// TagID names the tag bucket; empty for a category bucket.
	TagID string
	// Currency is the ISO 4217 code of the bucket.
	Currency string
	// Period is the rollup granularity.
	Period AnalyticsPeriod
	// PeriodStart is the truncated start of the bucket's period.
	PeriodStart time.Time
	// AmountMinor is the signed amount adjustment.
	AmountMinor int64
	// ExpenseCount is the signed expense-count adjustment.
	ExpenseCount int64
}

// BudgetUsage is spend accumulated against one budget period.
type BudgetUsage struct {
	// BudgetID identifies the budget the usage belongs to.
	BudgetID string
	// PeriodStart is the start of the budget period the spend falls in.
	PeriodStart time.Time
	// SpentMinor is the total spend in the period, in minor currency units.
	SpentMinor int64
	// AlertedPercent is the highest threshold already alerted on, suppressing
	// repeat alerts until spend falls back below it.
	AlertedPercent int
	// UpdatedAt is when the usage row was last recomputed.
	UpdatedAt time.Time
}

// AuditEntry is an immutable record of one domain event.
type AuditEntry struct {
	// EventID is the source event, used to make replay idempotent.
	EventID string
	// ActorID is the user who caused the event, when known.
	ActorID string
	// Action is the event type that produced the entry.
	Action string
	// ResourceType is the aggregate type the event belongs to.
	ResourceType string
	// ResourceID is the aggregate the event belongs to.
	ResourceID string
	// Metadata carries additional non-indexed context.
	Metadata map[string]string
	// OccurredAt is when the command produced the event.
	OccurredAt time.Time
}

// BudgetAlert describes a budget threshold that has just been crossed.
type BudgetAlert struct {
	// BudgetID identifies the budget that crossed a threshold.
	BudgetID string
	// OwnerID is the user to notify.
	OwnerID string
	// CategoryID is the budget's category.
	CategoryID string
	// Currency is the ISO 4217 code of the amounts.
	Currency string
	// BudgetName is the human-readable budget name for the notification.
	BudgetName string
	// SpentMinor is the spend that triggered the alert.
	SpentMinor int64
	// LimitMinor is the budget limit.
	LimitMinor int64
	// PercentUsed is the share of the limit consumed.
	PercentUsed int
	// Exceeded reports whether spend has passed the limit entirely.
	Exceeded bool
	// PeriodStart is the budget period the alert applies to.
	PeriodStart time.Time
}

// expenseEnvelope matches both the tagged wrappers ExpenseAdded/ExpenseUpdated
// use and the ExpenseRemoved body.
type expenseEnvelope struct {
	Expense   ExpenseRecord `json:"expense"`
	ExpenseID string        `json:"expense_id"`
	OwnerID   string        `json:"owner_id"`
}

// DecodeExpenseContribution reads an expense event body into the contribution it
// implies. It reports false for event types that carry no contribution, which
// callers must check before reading or reversing any previously applied state.
func DecodeExpenseContribution(eventType string, payload []byte) (ExpenseContribution, bool, error) {
	switch eventType {
	case "expense_added", "expense_updated", "expense_removed":
	default:
		return ExpenseContribution{}, false, nil
	}
	var envelope expenseEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ExpenseContribution{}, false, fmt.Errorf("decode %s payload: %w", eventType, err)
	}
	if eventType == "expense_removed" {
		id := envelope.ExpenseID
		if id == "" {
			id = envelope.Expense.ID
		}
		if id == "" {
			return ExpenseContribution{}, false, fmt.Errorf("%s payload has no expense id", eventType)
		}
		return ExpenseContribution{ExpenseID: id, OwnerID: envelope.OwnerID, Active: false}, true, nil
	}
	expense := envelope.Expense
	if expense.ID == "" {
		return ExpenseContribution{}, false, fmt.Errorf("%s payload has no expense id", eventType)
	}
	return ExpenseContribution{
		ExpenseID:   expense.ID,
		OwnerID:     expense.OwnerID,
		CategoryID:  expense.CategoryID,
		Currency:    expense.Currency,
		TagIDs:      append([]string(nil), expense.TagIDs...),
		AmountMinor: expense.AmountMinor,
		OccurredAt:  expense.OccurredAt.UTC(),
		Active:      expense.DeletedAt == nil,
	}, true, nil
}

// budgetEnvelope accepts both the tagged BudgetCreated/BudgetUpdated wrapper and
// the historical untagged BudgetRemoved body, whose fields serialised as Go
// identifiers because the struct carried no json tags.
type budgetEnvelope struct {
	Budget       BudgetRecord `json:"budget"`
	BudgetID     string       `json:"budget_id"`
	OwnerID      string       `json:"owner_id"`
	LegacyID     string       `json:"BudgetID"`
	LegacyOwner  string       `json:"OwnerID"`
	LegacyRemove time.Time    `json:"RemovedAt"`
}

// DecodeBudget reads a budget event body into the budget it refers to. It
// reports false for event types that carry no budget. Removal events yield a
// record holding only the identifiers, with DeletedAt set.
//
// The legacy untagged spelling of BudgetRemoved is accepted permanently:
// historical events are immutable, so their payloads can never be rewritten.
func DecodeBudget(eventType string, payload []byte) (BudgetRecord, bool, error) {
	switch eventType {
	case "budget_created", "budget_updated", "budget_removed":
	default:
		return BudgetRecord{}, false, nil
	}
	var envelope budgetEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return BudgetRecord{}, false, fmt.Errorf("decode %s payload: %w", eventType, err)
	}
	if eventType == "budget_removed" {
		id, owner, removed := envelope.BudgetID, envelope.OwnerID, envelope.LegacyRemove
		if id == "" {
			id, owner = envelope.LegacyID, envelope.LegacyOwner
		}
		if id == "" {
			id, owner = envelope.Budget.ID, envelope.Budget.OwnerID
		}
		if id == "" {
			return BudgetRecord{}, false, fmt.Errorf("%s payload has no budget id", eventType)
		}
		if removed.IsZero() {
			removed = time.Now().UTC()
		}
		return BudgetRecord{ID: id, OwnerID: owner, DeletedAt: &removed}, true, nil
	}
	if envelope.Budget.ID == "" {
		return BudgetRecord{}, false, fmt.Errorf("%s payload has no budget id", eventType)
	}
	return envelope.Budget, true, nil
}

// RollupPeriods lists the granularities every expense is rolled up into.
func RollupPeriods() []AnalyticsPeriod {
	return []AnalyticsPeriod{AnalyticsDay, AnalyticsWeek, AnalyticsMonth, AnalyticsYear}
}

// RollupPeriodStart truncates at to the start of period in UTC. Weeks start on
// Monday, matching PostgreSQL's date_trunc('week', ...).
func RollupPeriodStart(period AnalyticsPeriod, at time.Time) time.Time {
	at = at.UTC()
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case AnalyticsWeek:
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case AnalyticsMonth:
		return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	case AnalyticsYear:
		return time.Date(at.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}

// BudgetWindow returns the half-open period [start, end) of budget that
// contains at. A custom-period budget spans from its creation to its due date,
// or indefinitely when it has none.
func BudgetWindow(budget BudgetRecord, at time.Time) (time.Time, time.Time) {
	at = at.UTC()
	switch budget.Period {
	case BudgetDaily:
		start := RollupPeriodStart(AnalyticsDay, at)
		return start, start.AddDate(0, 0, 1)
	case BudgetWeekly:
		start := RollupPeriodStart(AnalyticsWeek, at)
		return start, start.AddDate(0, 0, 7)
	case BudgetYearly:
		start := RollupPeriodStart(AnalyticsYear, at)
		return start, start.AddDate(1, 0, 0)
	case BudgetCustom:
		start := RollupPeriodStart(AnalyticsDay, budget.CreatedAt)
		if budget.DueAt != nil {
			return start, budget.DueAt.UTC()
		}
		return start, start.AddDate(100, 0, 0)
	default:
		start := RollupPeriodStart(AnalyticsMonth, at)
		return start, start.AddDate(0, 1, 0)
	}
}

// BudgetThresholdCrossing reports the alert a spend change triggers, and false
// when the budget has already alerted at or above the level spent implies.
//
// Two rungs fire: the budget's configured threshold, and 100%. Spending back
// below the configured threshold clears the record so a later re-crossing
// alerts again; callers persist that through BudgetUsage.AlertedPercent.
func BudgetThresholdCrossing(budget BudgetRecord, usage BudgetUsage, spentMinor int64) (BudgetAlert, bool) {
	if budget.AmountLimitMinor <= 0 {
		return BudgetAlert{}, false
	}
	percent := int(spentMinor * 100 / budget.AmountLimitMinor)
	rung := 0
	switch {
	case percent >= 100:
		rung = 100
	case percent >= budget.ThresholdPercent:
		rung = budget.ThresholdPercent
	}
	if rung == 0 || rung <= usage.AlertedPercent {
		return BudgetAlert{}, false
	}
	return BudgetAlert{
		BudgetID:    budget.ID,
		OwnerID:     budget.OwnerID,
		CategoryID:  budget.CategoryID,
		Currency:    budget.Currency,
		BudgetName:  budget.Name,
		SpentMinor:  spentMinor,
		LimitMinor:  budget.AmountLimitMinor,
		PercentUsed: percent,
		Exceeded:    percent >= 100,
		PeriodStart: usage.PeriodStart,
	}, true
}

// AlertedPercentFor returns the threshold record to store for a spend level,
// clearing it once spend falls back below the budget's configured threshold.
func AlertedPercentFor(budget BudgetRecord, usage BudgetUsage, spentMinor int64) int {
	if budget.AmountLimitMinor <= 0 {
		return 0
	}
	percent := int(spentMinor * 100 / budget.AmountLimitMinor)
	switch {
	case percent >= 100:
		return 100
	case percent >= budget.ThresholdPercent:
		return max(usage.AlertedPercent, budget.ThresholdPercent)
	default:
		return 0
	}
}
