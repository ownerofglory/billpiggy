package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

func TestExpenseRepositoryListExpensesSortsByAmountWhenRequested(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewExpenseRepository()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	amounts := []int64{300, 900, 100, 500}
	for i, amount := range amounts {
		expense := domain.ExpenseRecord{
			ID: "expense-" + string(rune('a'+i)), OwnerID: "owner-1", Title: "Item", AmountMinor: amount,
			Currency: "EUR", OccurredAt: base.AddDate(0, 0, i), Status: domain.ExpenseConfirmed,
		}
		if err := repository.CreateExpense(ctx, expense); err != nil {
			t.Fatalf("create expense %d: %v", i, err)
		}
	}

	values, err := repository.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: "owner-1", SortBy: outbound.ExpenseSortAmount, Limit: 10})
	if err != nil {
		t.Fatalf("list expenses: %v", err)
	}
	if len(values) != len(amounts) {
		t.Fatalf("got %d expenses, want %d", len(values), len(amounts))
	}
	for i := 1; i < len(values); i++ {
		if values[i-1].AmountMinor < values[i].AmountMinor {
			t.Fatalf("expenses not sorted descending by amount: %#v", values)
		}
	}
	if values[0].AmountMinor != 900 || values[len(values)-1].AmountMinor != 100 {
		t.Fatalf("unexpected sort order: got amounts %d..%d", values[0].AmountMinor, values[len(values)-1].AmountMinor)
	}
}

func TestExpenseRepositoryListExpensesDefaultSortIsOccurredAtDescending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := memory.NewExpenseRepository()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := repository.CreateExpense(ctx, domain.ExpenseRecord{ID: "older", OwnerID: "owner-1", Title: "Older", AmountMinor: 900, Currency: "EUR", OccurredAt: base, Status: domain.ExpenseConfirmed}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if err := repository.CreateExpense(ctx, domain.ExpenseRecord{ID: "newer", OwnerID: "owner-1", Title: "Newer", AmountMinor: 100, Currency: "EUR", OccurredAt: base.AddDate(0, 0, 1), Status: domain.ExpenseConfirmed}); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	values, err := repository.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: "owner-1", Limit: 10})
	if err != nil {
		t.Fatalf("list expenses: %v", err)
	}
	if len(values) != 2 || values[0].ID != "newer" || values[1].ID != "older" {
		t.Fatalf("unexpected default order: %#v", values)
	}
}
