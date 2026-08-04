package cached_test

import (
	"context"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/cached"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// countingIdentityRepository wraps a memory repository and counts GetUserByID calls.
type countingIdentityRepository struct {
	*memory.IdentityRepository
	getUserByIDCalls int
}

func (r *countingIdentityRepository) GetUserByID(ctx context.Context, id string) (domain.AppUser, error) {
	r.getUserByIDCalls++
	return r.IdentityRepository.GetUserByID(ctx, id)
}

func TestCachedIdentityRepositoryCachesGetUserByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := &countingIdentityRepository{IdentityRepository: memory.NewIdentityRepository()}
	if err := inner.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", DisplayName: "User", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	repository := cached.NewIdentityRepository(inner, time.Minute)

	for i := 0; i < 3; i++ {
		user, err := repository.GetUserByID(ctx, "user-1")
		if err != nil || user.Email != "user@example.com" {
			t.Fatalf("get user: %#v, %v", user, err)
		}
	}
	if inner.getUserByIDCalls != 1 {
		t.Fatalf("underlying calls = %d, want 1 (subsequent reads should hit the cache)", inner.getUserByIDCalls)
	}
}

func TestCachedIdentityRepositoryInvalidatesOnUpdateAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := &countingIdentityRepository{IdentityRepository: memory.NewIdentityRepository()}
	if err := inner.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", DisplayName: "Old Name", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	repository := cached.NewIdentityRepository(inner, time.Minute)
	if _, err := repository.GetUserByID(ctx, "user-1"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	if err := repository.UpdateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", DisplayName: "New Name", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("update user: %v", err)
	}
	updated, err := repository.GetUserByID(ctx, "user-1")
	if err != nil || updated.DisplayName != "New Name" {
		t.Fatalf("expected the cache to reflect the update: %#v, %v", updated, err)
	}
	if inner.getUserByIDCalls != 2 {
		t.Fatalf("underlying calls = %d, want 2 (initial read + re-read after invalidation)", inner.getUserByIDCalls)
	}

	if err := repository.DeleteUser(ctx, "user-1"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := repository.GetUserByID(ctx, "user-1"); err == nil {
		t.Fatal("expected an error reading a deleted, invalidated user")
	}
}

func TestCachedIdentityRepositoryOtherMethodsDelegateThrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := memory.NewIdentityRepository()
	repository := cached.NewIdentityRepository(inner, time.Minute)
	if err := repository.CreateUser(ctx, domain.AppUser{ID: "user-1", Email: "user@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	users, err := repository.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("list users: %#v, %v", users, err)
	}
	byEmail, err := repository.GetUserByEmail(ctx, "user@example.com")
	if err != nil || byEmail.ID != "user-1" {
		t.Fatalf("get user by email: %#v, %v", byEmail, err)
	}
}
