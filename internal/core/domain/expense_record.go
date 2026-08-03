package domain

import "time"

// ExpenseStatus communicates an expense's sharing and reimbursement state.
type ExpenseStatus string

const (
	ExpenseDraft      ExpenseStatus = "draft"
	ExpenseConfirmed  ExpenseStatus = "confirmed"
	ExpenseShared     ExpenseStatus = "shared"
	ExpenseReimbursed ExpenseStatus = "reimbursed"
)

// ExpenseRecord is the expense write and read model. Monetary values are stored in
// minor currency units to avoid floating-point loss.
type ExpenseRecord struct {
	ID               string
	OwnerID          string
	Title            string
	AmountMinor      int64
	Currency         string
	OccurredAt       time.Time
	CategoryID       string
	CategoryName     string
	TagIDs           []string
	Status           ExpenseStatus
	SharedGroupID    string
	Items            []ExpenseItem
	Latitude         *float64
	Longitude        *float64
	Address          string
	ReceiptObjectKey string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// ExpenseItem is a line item parsed from or entered for a receipt.
type ExpenseItem struct {
	Title       string
	Quantity    string
	AmountMinor int64
}

// ExpenseAdded is emitted once when an expense is created.
type ExpenseAdded struct {
	Expense ExpenseRecord `json:"expense"`
}

// ExpenseUpdated is emitted for every authorized edit.
type ExpenseUpdated struct {
	Expense ExpenseRecord `json:"expense"`
}

// ExpenseRemoved is emitted instead of hard-deleting an expense.
type ExpenseRemoved struct {
	ExpenseID string    `json:"expense_id"`
	OwnerID   string    `json:"owner_id"`
	RemovedAt time.Time `json:"removed_at"`
}
