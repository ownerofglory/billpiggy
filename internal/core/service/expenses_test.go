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

func TestExpenseServiceSharedGroupVisibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, events := memory.NewExpenseRepository(), memory.NewEventStore()
	groups := memory.NewGroupRepository()
	expenses, err := service.NewExpenseService(repository, events, memory.NewUnitOfWork(repository, events))
	if err != nil {
		t.Fatalf("new expense service: %v", err)
	}
	expenses = expenses.WithGroups(groups)

	if err := groups.CreateGroup(ctx, domain.UserGroup{ID: "group-1", CreatedBy: "owner-1", MemberIDs: []string{"owner-1", "member-2"}}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	shared, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Shared dinner", AmountMinor: 4500, Currency: "EUR", OccurredAt: time.Now(), SharedGroupID: "group-1",
	})
	if err != nil {
		t.Fatalf("create shared expense: %v", err)
	}
	private, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Private coffee", AmountMinor: 350, Currency: "EUR", OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create private expense: %v", err)
	}

	// A fellow group member sees the shared expense but not the private one.
	member := domain.AppUser{ID: "member-2", Role: domain.RoleMember}
	got, err := expenses.GetExpenseForViewer(ctx, member, shared.ID)
	if err != nil || got.ID != shared.ID {
		t.Fatalf("member viewing shared expense: %#v, %v", got, err)
	}
	if _, err := expenses.GetExpenseForViewer(ctx, member, private.ID); err != service.ErrNotFound {
		t.Fatalf("member viewing private expense: err = %v, want ErrNotFound", err)
	}
	listed, err := expenses.ListExpensesForViewer(ctx, member, outbound.ExpenseListFilter{})
	if err != nil || len(listed) != 1 || listed[0].ID != shared.ID {
		t.Fatalf("member list = %#v, %v, want only the shared expense", listed, err)
	}

	// An outsider with no group membership sees neither.
	outsider := domain.AppUser{ID: "someone-else", Role: domain.RoleMember}
	if _, err := expenses.GetExpenseForViewer(ctx, outsider, shared.ID); err != service.ErrNotFound {
		t.Fatalf("outsider viewing shared expense: err = %v, want ErrNotFound", err)
	}

	// Without WithGroups configured, shared visibility does not apply.
	repository2, events2 := memory.NewExpenseRepository(), memory.NewEventStore()
	unconfigured, err := service.NewExpenseService(repository2, events2, memory.NewUnitOfWork(repository2, events2))
	if err != nil {
		t.Fatalf("new expense service: %v", err)
	}
	if _, err := unconfigured.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{Title: "x", AmountMinor: 100, Currency: "EUR", OccurredAt: time.Now(), SharedGroupID: "group-1"}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := unconfigured.GetExpenseForViewer(ctx, member, shared.ID); err != service.ErrNotFound {
		t.Fatalf("unconfigured groups: err = %v, want ErrNotFound (no visibility without WithGroups)", err)
	}
}

func TestExpenseServiceValidatesCategoryAndTagOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, events := memory.NewExpenseRepository(), memory.NewEventStore()
	taxonomy := memory.NewTaxonomyRepository()
	expenses, err := service.NewExpenseService(repository, events, memory.NewUnitOfWork(repository, events))
	if err != nil {
		t.Fatalf("new expense service: %v", err)
	}
	expenses = expenses.WithTaxonomy(taxonomy)

	if err := taxonomy.CreateCategory(ctx, "owner-1", domain.ExpenseCategory{ID: "cat-1", Name: "Travel"}); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if err := taxonomy.CreateTag(ctx, "owner-1", domain.ExpenseTag{ID: "tag-1", Name: "Family"}); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	// Owner-1's own category and tag are accepted.
	if _, err := expenses.CreateExpense(ctx, "owner-1", service.CreateExpenseCommand{
		Title: "Trip", AmountMinor: 1000, Currency: "EUR", OccurredAt: time.Now(), CategoryID: "cat-1", TagIDs: []string{"tag-1"},
	}); err != nil {
		t.Fatalf("create expense with owned category/tag: %v", err)
	}

	// owner-2 cannot reference owner-1's private category or tag.
	if _, err := expenses.CreateExpense(ctx, "owner-2", service.CreateExpenseCommand{
		Title: "Trip", AmountMinor: 1000, Currency: "EUR", OccurredAt: time.Now(), CategoryID: "cat-1",
	}); err != service.ErrForbidden {
		t.Fatalf("create with someone else's category: err = %v, want ErrForbidden", err)
	}
	if _, err := expenses.CreateExpense(ctx, "owner-2", service.CreateExpenseCommand{
		Title: "Trip", AmountMinor: 1000, Currency: "EUR", OccurredAt: time.Now(), TagIDs: []string{"tag-1"},
	}); err != service.ErrForbidden {
		t.Fatalf("create with someone else's tag: err = %v, want ErrForbidden", err)
	}

	// A default (system) category has no owner and is accepted by anyone.
	for _, defaultCategory := range domain.DefaultCategories() {
		if _, err := expenses.CreateExpense(ctx, "owner-2", service.CreateExpenseCommand{
			Title: "Default category expense", AmountMinor: 500, Currency: "EUR", OccurredAt: time.Now(), CategoryID: defaultCategory.ID,
		}); err != nil {
			t.Fatalf("create with default category: %v", err)
		}
		break
	}
}
