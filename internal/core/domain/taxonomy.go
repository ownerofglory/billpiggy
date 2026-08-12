package domain

import "time"

// ExpenseCategory groups expenses and may be a system default or user-owned value.
type ExpenseCategory struct {
	// ID identifies the category.
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// Color is an optional hex swatch used by clients.
	Color string `json:"color"`
	// IsDefault marks a category the application ships for every user.
	IsDefault bool `json:"isDefault"`
	// CreatedAt is when the category was created.
	CreatedAt time.Time `json:"createdAt"`
}

// ExpenseTag is a user-owned label that can be attached to expenses.
type ExpenseTag struct {
	// ID identifies the tag.
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// Color is an optional hex swatch used by clients.
	Color string `json:"color"`
	// CreatedAt is when the tag was created.
	CreatedAt time.Time `json:"createdAt"`
}

// DefaultCategories returns the categories every user starts with.
//
// The identifiers are fixed UUIDs so the in-memory adapter and the PostgreSQL
// seed (migration 000008, extended by 000018) agree: an expense created
// against a default category in development must reference the same row in
// production. Adding a category means appending a new ID here and a new
// migration to insert it — never editing 000008 itself, since it has almost
// certainly already run against every existing database.
func DefaultCategories() []ExpenseCategory {
	return []ExpenseCategory{
		{ID: "0197c1a0-0000-4000-8000-000000000001", Name: "Food", Color: "#f97316", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000002", Name: "Groceries", Color: "#84cc16", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000003", Name: "Transport", Color: "#0ea5e9", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000004", Name: "Home", Color: "#8b5cf6", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000005", Name: "Utilities", Color: "#14b8a6", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000006", Name: "Health", Color: "#ef4444", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000007", Name: "Entertainment", Color: "#ec4899", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000008", Name: "Other", Color: "#64748b", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-000000000009", Name: "Pets", Color: "#f59e0b", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-00000000000a", Name: "Education", Color: "#6366f1", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-00000000000b", Name: "Insurance", Color: "#3b82f6", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-00000000000c", Name: "Travel", Color: "#06b6d4", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-00000000000d", Name: "Personal Care", Color: "#d946ef", IsDefault: true},
		{ID: "0197c1a0-0000-4000-8000-00000000000e", Name: "Investments", Color: "#22c55e", IsDefault: true},
	}
}
