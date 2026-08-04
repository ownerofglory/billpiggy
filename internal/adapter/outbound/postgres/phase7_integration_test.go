//go:build integration

package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// randomHex returns a random hex-encoded token hash, matching the format
// IdentityRepository expects for RefreshToken.TokenHash.
func randomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate random token: %v", err)
	}
	return hex.EncodeToString(buf)
}

func TestExpenseRepositorySharedGroupVisibility(t *testing.T) {
	pool := newPool(t)
	expenses := postgresadapter.NewExpenseRepository(pool)
	groups := postgresadapter.NewGroupRepository(pool)
	ctx := context.Background()

	owner := seedUser(t, pool, "owner@example.test")
	member := seedUser(t, pool, "member@example.test")
	outsider := seedUser(t, pool, "outsider@example.test")
	category := defaultCategoryID(t, pool, "Food")

	group := domain.UserGroup{ID: uuid.NewString(), Name: "Roommates", CreatedBy: owner, CreatedAt: time.Now(), MemberIDs: []string{owner, member}}
	if err := groups.CreateGroup(ctx, group); err != nil {
		t.Fatalf("create group: %v", err)
	}

	shared := expenseRecord(owner, category, 1000, time.Now())
	shared.SharedGroupID = group.ID
	if err := expenses.CreateExpense(ctx, shared); err != nil {
		t.Fatalf("create shared expense: %v", err)
	}
	private := expenseRecord(owner, category, 500, time.Now())
	if err := expenses.CreateExpense(ctx, private); err != nil {
		t.Fatalf("create private expense: %v", err)
	}

	// The fellow member sees the shared expense but not the private one.
	got, err := expenses.GetExpenseVisible(ctx, member, shared.ID, []string{group.ID})
	if err != nil || got.ID != shared.ID {
		t.Fatalf("member viewing shared expense: %#v, %v", got, err)
	}
	if _, err := expenses.GetExpenseVisible(ctx, member, private.ID, []string{group.ID}); err == nil {
		t.Fatal("member should not see the private expense")
	}

	listed, err := expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: member, SharedGroupIDs: []string{group.ID}, Limit: 25})
	if err != nil || len(listed) != 1 || listed[0].ID != shared.ID {
		t.Fatalf("member list = %#v, %v, want only the shared expense", listed, err)
	}

	// An outsider with no group membership sees neither, even asking directly.
	if _, err := expenses.GetExpenseVisible(ctx, outsider, shared.ID, nil); err == nil {
		t.Fatal("outsider should not see the shared expense without group membership")
	}
}

func TestTaxonomyRepositoryUpdateAndDeleteAreOwnerScoped(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewTaxonomyRepository(pool)
	ctx := context.Background()
	owner := seedUser(t, pool, "tax-owner@example.test")
	other := seedUser(t, pool, "tax-other@example.test")

	category := domain.ExpenseCategory{ID: uuid.NewString(), Name: "Travel", Color: "#ff0000"}
	if err := repository.CreateCategory(ctx, owner, category); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if err := repository.UpdateCategory(ctx, other, domain.ExpenseCategory{ID: category.ID, Name: "Hijacked"}); err == nil {
		t.Fatal("expected an error updating another owner's category")
	}
	if err := repository.UpdateCategory(ctx, owner, domain.ExpenseCategory{ID: category.ID, Name: "Vacation", Color: "#00ff00"}); err != nil {
		t.Fatalf("update category: %v", err)
	}
	categories, err := repository.ListCategories(ctx, owner)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	found := false
	for _, value := range categories {
		if value.ID == category.ID && value.Name == "Vacation" {
			found = true
		}
	}
	if !found {
		t.Fatal("update was not applied")
	}
	if err := repository.DeleteCategory(ctx, other, category.ID); err == nil {
		t.Fatal("expected an error deleting another owner's category")
	}
	if err := repository.DeleteCategory(ctx, owner, category.ID); err != nil {
		t.Fatalf("delete category: %v", err)
	}

	tag := domain.ExpenseTag{ID: uuid.NewString(), Name: "Family"}
	if err := repository.CreateTag(ctx, owner, tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := repository.UpdateTag(ctx, owner, domain.ExpenseTag{ID: tag.ID, Name: "Friends"}); err != nil {
		t.Fatalf("update tag: %v", err)
	}
	if err := repository.DeleteTag(ctx, owner, tag.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
}

func TestGroupRepositoryUpdateDeleteAndMembership(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewGroupRepository(pool)
	ctx := context.Background()
	owner := seedUser(t, pool, "group-owner@example.test")
	member := seedUser(t, pool, "group-member@example.test")

	group := domain.UserGroup{ID: uuid.NewString(), Name: "Household", CreatedBy: owner, CreatedAt: time.Now()}
	if err := repository.CreateGroup(ctx, group); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := repository.UpdateGroup(ctx, group.ID, "Roommates"); err != nil {
		t.Fatalf("update group: %v", err)
	}
	fetched, err := repository.GetGroup(ctx, group.ID)
	if err != nil || fetched.Name != "Roommates" {
		t.Fatalf("get group: %#v, %v", fetched, err)
	}

	if err := repository.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("add member: %v", err)
	}
	fetched, err = repository.GetGroup(ctx, group.ID)
	if err != nil || len(fetched.MemberIDs) != 1 || fetched.MemberIDs[0] != member {
		t.Fatalf("get group after add: %#v, %v", fetched, err)
	}
	// Adding an already-present member is a no-op, not an error.
	if err := repository.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("re-add member: %v", err)
	}

	if err := repository.RemoveMember(ctx, group.ID, member); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	fetched, err = repository.GetGroup(ctx, group.ID)
	if err != nil || len(fetched.MemberIDs) != 0 {
		t.Fatalf("get group after remove: %#v, %v", fetched, err)
	}

	if err := repository.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := repository.GetGroup(ctx, group.ID); err == nil {
		t.Fatal("expected an error reading a deleted group")
	}
}

func TestIdentityRepositoryRevokeAllRefreshTokens(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	ctx := context.Background()
	owner := seedUser(t, pool, "revoke-owner@example.test")

	first := domain.RefreshToken{ID: uuid.NewString(), UserID: owner, TokenHash: randomHex(t), FamilyID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	second := domain.RefreshToken{ID: uuid.NewString(), UserID: owner, TokenHash: randomHex(t), FamilyID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := repository.CreateRefreshToken(ctx, first); err != nil {
		t.Fatalf("create first token: %v", err)
	}
	if err := repository.CreateRefreshToken(ctx, second); err != nil {
		t.Fatalf("create second token: %v", err)
	}

	if err := repository.RevokeAllRefreshTokens(ctx, owner); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	for _, token := range []domain.RefreshToken{first, second} {
		reloaded, err := repository.GetRefreshTokenByHash(ctx, token.TokenHash)
		if err != nil {
			t.Fatalf("reload token: %v", err)
		}
		if reloaded.RevokedAt == nil {
			t.Fatalf("token %s was not revoked", token.ID)
		}
	}
}
