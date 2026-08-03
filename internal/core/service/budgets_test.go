package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestBudgetServiceRecordsBudgetLifecycleEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	events := memory.NewEventStore()
	groupRepository := memory.NewGroupRepository()
	repository := memory.NewBudgetRepository()
	budgets, err := service.NewBudgetService(repository, events, groupRepository, memory.NewUnitOfWork(repository, events))
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.AppUser{ID: "owner", Role: domain.RoleMember}
	created, err := budgets.CreateBudget(ctx, owner, domain.BudgetRecord{Name: "Food", CategoryID: "food", AmountLimitMinor: 50_00, Currency: "eur", ThresholdPercent: 80, Period: domain.BudgetMonthly})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	updated, err := budgets.UpdateBudget(ctx, owner, created.ID, domain.BudgetRecord{Name: "Food and coffee", CategoryID: "coffee", AmountLimitMinor: 60_00, Currency: "eur", ThresholdPercent: 90, Period: domain.BudgetMonthly})
	if err != nil {
		t.Fatalf("update budget: %v", err)
	}
	if updated.CategoryID != "coffee" || updated.AmountLimitMinor != 60_00 {
		t.Fatalf("updated budget = %#v", updated)
	}
	if err := budgets.DeleteBudget(ctx, owner, created.ID); err != nil {
		t.Fatalf("delete budget: %v", err)
	}
	if values, err := budgets.ListBudgets(ctx, owner); err != nil || len(values) != 0 {
		t.Fatalf("list after delete = %#v, %v", values, err)
	}
	if got := len(events.Events()); got != 3 {
		t.Fatalf("events = %d, want 3", got)
	}
}

func TestBudgetServiceMakesSharedBudgetsVisibleToGroupMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	groupRepository := memory.NewGroupRepository()
	if err := groupRepository.CreateGroup(ctx, domain.UserGroup{ID: "family", Name: "Family", CreatedBy: "admin", MemberIDs: []string{"member"}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repository, events := memory.NewBudgetRepository(), memory.NewEventStore()
	budgets, err := service.NewBudgetService(repository, events, groupRepository, memory.NewUnitOfWork(repository, events))
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.AppUser{ID: "owner", Role: domain.RoleMember}
	if _, err := budgets.CreateBudget(ctx, owner, domain.BudgetRecord{Name: "Shared food", CategoryID: "food", AmountLimitMinor: 100_00, Currency: "EUR", ThresholdPercent: 80, Period: domain.BudgetMonthly, SharedGroupID: "family"}); err == nil {
		t.Fatal("owner should not be able to share with an unrelated group")
	}
	admin := domain.AppUser{ID: "admin", Role: domain.RoleAdmin}
	created, err := budgets.CreateBudget(ctx, admin, domain.BudgetRecord{Name: "Shared food", CategoryID: "food", AmountLimitMinor: 100_00, Currency: "EUR", ThresholdPercent: 80, Period: domain.BudgetMonthly, SharedGroupID: "family"})
	if err != nil {
		t.Fatalf("create shared budget: %v", err)
	}
	member := domain.AppUser{ID: "member", Role: domain.RoleMember}
	values, err := budgets.ListBudgets(ctx, member)
	if err != nil || len(values) != 1 || values[0].ID != created.ID {
		t.Fatalf("member budgets = %#v, %v", values, err)
	}
}
