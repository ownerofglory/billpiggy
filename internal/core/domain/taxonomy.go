package domain

import "time"

// ExpenseCategory groups expenses and may be a system default or user-owned value.
type ExpenseCategory struct {
	ID, Name, Color string
	IsDefault       bool
	CreatedAt       time.Time
}

// ExpenseTag is a user-owned label that can be attached to expenses.
type ExpenseTag struct {
	ID, Name, Color string
	CreatedAt       time.Time
}
