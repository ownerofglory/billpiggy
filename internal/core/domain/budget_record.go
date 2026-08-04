package domain

import "time"

// BudgetPeriod describes the period a budget covers.
type BudgetPeriod string

const (
	// BudgetDaily applies to each calendar day.
	BudgetDaily BudgetPeriod = "daily"
	// BudgetWeekly applies to each calendar week.
	BudgetWeekly BudgetPeriod = "weekly"
	// BudgetMonthly applies to each calendar month.
	BudgetMonthly BudgetPeriod = "monthly"
	// BudgetYearly applies to each calendar year.
	BudgetYearly BudgetPeriod = "yearly"
	// BudgetCustom applies until its configured due date.
	BudgetCustom BudgetPeriod = "custom"
)

// BudgetRecord is the budget projection stored in minor currency units.
type BudgetRecord struct {
	// ID identifies the budget.
	ID            string `json:"id"`
	OwnerID       string `json:"ownerID"`
	CategoryID    string `json:"categoryID"`
	Name          string `json:"name"`
	Currency      string `json:"currency"`
	SharedGroupID string `json:"sharedGroupID"`
	// AmountLimitMinor is the limit in the currency's minor units.
	AmountLimitMinor int64 `json:"amountLimitMinor"`
	// ThresholdPercent triggers an alert when this percentage is reached.
	ThresholdPercent int `json:"thresholdPercent"`
	// Period defines the budget window.
	Period BudgetPeriod `json:"period"`
	// DueAt optionally caps a custom budget.
	DueAt *time.Time `json:"dueAt"`
	// CreatedAt and UpdatedAt record projection timestamps.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// DeletedAt marks a soft-deleted budget.
	DeletedAt *time.Time `json:"deletedAt"`
}

// BudgetCreated records creation of a category-scoped budget.
type BudgetCreated struct {
	Budget BudgetRecord `json:"budget"`
}

// BudgetUpdated records an authorized budget change.
type BudgetUpdated struct {
	Budget BudgetRecord `json:"budget"`
}

// BudgetRemoved records soft deletion of a budget.
//
// The json tags match ExpenseRemoved. Events written before they were added
// serialised these fields as Go identifiers, so [DecodeBudget] accepts both
// spellings permanently.
type BudgetRemoved struct {
	// BudgetID identifies the removed budget.
	BudgetID string `json:"budget_id"`
	// OwnerID is the user the budget belonged to.
	OwnerID string `json:"owner_id"`
	// RemovedAt is when the budget was soft-deleted.
	RemovedAt time.Time `json:"removed_at"`
}
