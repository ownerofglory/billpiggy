package report

import (
	"strconv"

	"github.com/shopspring/decimal"
)

// formatAmount renders minor currency units (e.g. cents) as a fixed
// two-decimal amount, e.g. 1234 -> "12.34".
func formatAmount(amountMinor int64) string {
	return decimal.New(amountMinor, -2).StringFixed(2)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
