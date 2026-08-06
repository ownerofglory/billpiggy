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
	ID               string        `json:"id"`
	OwnerID          string        `json:"ownerID"`
	Title            string        `json:"title"`
	AmountMinor      int64         `json:"amountMinor"`
	Currency         string        `json:"currency"`
	OccurredAt       time.Time     `json:"occurredAt"`
	CategoryID       string        `json:"categoryID"`
	CategoryName     string        `json:"categoryName"`
	TagIDs           []string      `json:"tagIDs"`
	Status           ExpenseStatus `json:"status"`
	SharedGroupID    string        `json:"sharedGroupID"`
	Items            []ExpenseItem `json:"items"`
	Latitude         *float64      `json:"latitude"`
	Longitude        *float64      `json:"longitude"`
	Address          string        `json:"address"`
	ReceiptObjectKey string        `json:"receiptObjectKey"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	DeletedAt        *time.Time    `json:"deletedAt"`
}

// ExpenseItem is a line item parsed from or entered for a receipt.
type ExpenseItem struct {
	Title       string `json:"title"`
	Quantity    string `json:"quantity"`
	AmountMinor int64  `json:"amountMinor"`
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
