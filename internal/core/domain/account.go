package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// ReportPeriod represents time period category for report
type ReportPeriod string

const (
	MonthlyReport ReportPeriod = "monthly"
	WeeklyReport  ReportPeriod = "weekly"
	DailyReport   ReportPeriod = "daily"
)

// Account summarizes user's expenses
type Account struct {
	CurrentDaily   ExpensesCount
	CurrentWeekly  ExpensesCount
	CurrentMonthly ExpensesCount
}

// ExpenseReport represents report for a given period of time
type ExpenseReport struct {
	// Report period category
	Category ReportPeriod
	// Count of expense record
	Count ExpensesCount
	// First day of the report period
	Time time.Time
}

// ExpensesCount represents count and total amount
type ExpensesCount struct {
	// Count of expense records
	Count uint
	// Total accumulated expense amount
	Total decimal.Decimal
}
