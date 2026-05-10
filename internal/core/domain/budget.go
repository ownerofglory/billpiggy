package domain

import (
	"github.com/shopspring/decimal"
)

// BudgetPeriod represents time period category
type BudgetPeriod string

const (
	DailyBudget   BudgetPeriod = "daily"
	MonthlyBudget BudgetPeriod = "monthly"
	YearlyBudget  BudgetPeriod = "yearly"
)

// Budget represents a budget for spending over given time for chosen categories
type Budget struct {
	// Budget name
	Name string
	// Period category
	Period BudgetPeriod
	// spending limit
	Limit decimal.Decimal
	// Alert threshold in percents
	AlertThreshold int
	// Expense category
	Categories []Category
}
