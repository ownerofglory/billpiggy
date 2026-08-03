package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestExpenseServiceLifecycleAndOwnerScopedFiltering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, events := memory.NewExpenseRepository(), memory.NewEventStore()
	expenses, err := service.NewExpenseService(repository, events, memory.NewUnitOfWork(repository, events))
	if err != nil {
		t.Fatalf("new expense service: %v", err)
	}
	created, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", AmountMinor: 2500, Currency: "eur", OccurredAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		CategoryID: "entertainment", CategoryName: "Entertainment", TagIDs: []string{"family", "movie"}, Items: []domain.ExpenseItem{{Title: "Toy Story", Quantity: "2", AmountMinor: 1400}},
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if created.Status != domain.ExpenseConfirmed {
		t.Fatalf("status = %q, want confirmed", created.Status)
	}
	if len(events.Events()) != 1 || events.Events()[0].EventType != "expense_added" {
		t.Fatalf("unexpected events: %#v", events.Events())
	}

	updated, err := expenses.UpdateExpense(ctx, "owner-1", created.ID, service.UpdateExpenseCommand{
		Title: "Cinema and popcorn", AmountMinor: 3600, Currency: "EUR", OccurredAt: created.OccurredAt, CategoryID: created.CategoryID, CategoryName: created.CategoryName, TagIDs: created.TagIDs,
	})
	if err != nil {
		t.Fatalf("update expense: %v", err)
	}
	if updated.AmountMinor != 3600 {
		t.Fatalf("amount = %d, want 3600", updated.AmountMinor)
	}
	listed, err := expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: "owner-1", Query: "popcorn", TagIDs: []string{"movie"}})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list expenses = %#v, %v", listed, err)
	}
	otherOwner, err := expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: "owner-2"})
	if err != nil || len(otherOwner) != 0 {
		t.Fatalf("other owner list = %#v, %v", otherOwner, err)
	}
	if err := expenses.DeleteExpense(ctx, "owner-1", created.ID); err != nil {
		t.Fatalf("delete expense: %v", err)
	}
	if len(events.Events()) != 3 || events.Events()[2].EventType != "expense_removed" {
		t.Fatalf("unexpected deletion events: %#v", events.Events())
	}
}
