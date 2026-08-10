package domain

import "time"

// ExtractedExpenseItem is one line item within an AI-suggested expense.
type ExtractedExpenseItem struct {
	// Title is what this line item was for.
	Title string `json:"title" jsonschema_description:"What this line item was for"`
	// AmountMinor is the item's amount in minor currency units.
	AmountMinor int64 `json:"amount_minor" jsonschema_description:"Amount in minor currency units, e.g. 1100 for 11.00"`
}

// ExtractedExpense is an AI-suggested expense pending user confirmation.
//
// Nothing is persisted from it automatically: extraction can misread a
// receipt or misparse a sentence, so the user reviews it and submits it
// through the ordinary expense-creation endpoint rather than the AI
// committing state on their behalf.
type ExtractedExpense struct {
	// Title is a short human title for the expense.
	Title string `json:"title" jsonschema_description:"A short human title for the expense, e.g. Cinema"`
	// AmountMinor is the total amount in minor currency units.
	AmountMinor int64 `json:"amount_minor" jsonschema_description:"Total amount in minor currency units, e.g. 2500 for 25.00"`
	// Currency is the ISO 4217 currency code.
	Currency string `json:"currency" jsonschema_description:"ISO 4217 currency code, e.g. EUR"`
	// OccurredAt is when the expense happened.
	OccurredAt time.Time `json:"occurred_at" jsonschema_description:"When the expense happened, RFC3339. Use the current date if not stated."`
	// CategoryName is a free-text category guess.
	CategoryName string `json:"category_name" jsonschema_description:"A short category guess, e.g. Entertainment"`
	// Items breaks the total down when more than one thing was mentioned. Empty
	// when only one thing was mentioned; strict structured-output schemas
	// require every property to be present, so this can't be omitempty.
	Items []ExtractedExpenseItem `json:"items" jsonschema_description:"Individual line items if more than one was mentioned"`
}
