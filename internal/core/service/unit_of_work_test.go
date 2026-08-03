package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// failingExpenseRepository fails its writes so a service can be observed
// rolling back the event it appended alongside them.
type failingExpenseRepository struct {
	outbound.ExpenseRepository
	err error
}

func (r failingExpenseRepository) CreateExpense(context.Context, domain.ExpenseRecord) error {
	return r.err
}

func TestCreateExpenseDiscardsTheEventWhenTheProjectionFails(t *testing.T) {
	t.Parallel()
	// This is the defect the unit of work exists to prevent: the event and the
	// read-model row used to commit in separate transactions, so a failure of
	// the second left an event with no projection behind it.
	events := memory.NewEventStore()
	repository := failingExpenseRepository{ExpenseRepository: memory.NewExpenseRepository(), err: errors.New("projection unavailable")}
	unit := memory.NewUnitOfWork(events)
	events.WithUnitOfWork(unit)

	expenses, err := service.NewExpenseService(repository, events, unit)
	if err != nil {
		t.Fatalf("build expense service: %v", err)
	}

	_, err = expenses.CreateExpense(context.Background(), "owner-1", service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err == nil {
		t.Fatal("expected the failing projection write to fail the command")
	}
	if recorded := events.Events(); len(recorded) != 0 {
		t.Fatalf("event survived a failed projection write: %#v", recorded)
	}
}

func TestUnitOfWorkRestoresEveryParticipant(t *testing.T) {
	t.Parallel()
	expenses := memory.NewExpenseRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenses, events)

	expense := domain.ExpenseRecord{
		ID: "expense-1", OwnerID: "owner-1", Title: "Cinema", Currency: "EUR",
		AmountMinor: 25_00, OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	}
	failure := errors.New("later step failed")
	err := unit.Within(context.Background(), func(ctx context.Context) error {
		if err := expenses.CreateExpense(ctx, expense); err != nil {
			return err
		}
		if err := events.Append(ctx, outbound.DomainEvent{ID: "event-1", AggregateType: "expense", AggregateID: expense.ID, EventType: "expense_added"}); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Within returned %v, want %v", err, failure)
	}
	if _, err := expenses.GetExpense(context.Background(), "owner-1", expense.ID); err == nil {
		t.Fatal("expense survived a rolled-back unit of work")
	}
	if recorded := events.Events(); len(recorded) != 0 {
		t.Fatalf("event survived a rolled-back unit of work: %#v", recorded)
	}
}

func TestUnitOfWorkCommitsWhenEveryStepSucceeds(t *testing.T) {
	t.Parallel()
	expenses := memory.NewExpenseRepository()
	unit := memory.NewUnitOfWork(expenses)
	expense := domain.ExpenseRecord{
		ID: "expense-1", OwnerID: "owner-1", Title: "Cinema", Currency: "EUR",
		AmountMinor: 25_00, OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	}
	if err := unit.Within(context.Background(), func(ctx context.Context) error {
		return expenses.CreateExpense(ctx, expense)
	}); err != nil {
		t.Fatalf("Within: %v", err)
	}
	if _, err := expenses.GetExpense(context.Background(), "owner-1", expense.ID); err != nil {
		t.Fatalf("expense missing after a committed unit of work: %v", err)
	}
}

func TestUnitOfWorkNestsWithoutDeadlocking(t *testing.T) {
	t.Parallel()
	// Services call each other's ports; a nested Within must join the enclosing
	// unit rather than block on its own lock.
	expenses := memory.NewExpenseRepository()
	unit := memory.NewUnitOfWork(expenses)
	done := make(chan error, 1)
	go func() {
		done <- unit.Within(context.Background(), func(ctx context.Context) error {
			return unit.Within(ctx, func(context.Context) error { return nil })
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested Within: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested Within deadlocked")
	}
}
