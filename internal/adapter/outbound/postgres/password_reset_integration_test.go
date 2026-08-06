//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// sha256Hex mirrors AuthService's unexported tokenHash so these tests can
// exercise the adapter directly against a chosen token hash, exactly as
// TestGetRefreshTokenByHash and the invitation tests seed their own rows.
func sha256Hex(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func TestIdentityRepositoryPasswordResetCreateGetMarkUsed(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	owner := seedUser(t, pool, "password-reset-owner@example.test")
	ctx := context.Background()

	reset := domain.PasswordReset{
		ID: uuid.NewString(), UserID: owner, TokenHash: sha256Hex("a-raw-reset-token"),
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	if err := repository.CreatePasswordReset(ctx, reset); err != nil {
		t.Fatalf("create password reset: %v", err)
	}

	fetched, err := repository.GetPasswordResetByTokenHash(ctx, reset.TokenHash)
	if err != nil {
		t.Fatalf("get password reset: %v", err)
	}
	if fetched.UserID != owner || fetched.UsedAt != nil {
		t.Fatalf("fetched = %#v, want unused and owned by %s", fetched, owner)
	}

	if err := repository.MarkPasswordResetUsed(ctx, reset.ID); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	reread, err := repository.GetPasswordResetByTokenHash(ctx, reset.TokenHash)
	if err != nil {
		t.Fatalf("re-read password reset: %v", err)
	}
	if reread.UsedAt == nil {
		t.Fatal("UsedAt should be set after MarkPasswordResetUsed")
	}
}

func TestIdentityRepositoryInvalidatePendingPasswordResets(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	owner := seedUser(t, pool, "password-reset-invalidate@example.test")
	otherOwner := seedUser(t, pool, "password-reset-other@example.test")
	ctx := context.Background()

	mine := domain.PasswordReset{ID: uuid.NewString(), UserID: owner, TokenHash: sha256Hex("mine"), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	alsoMine := domain.PasswordReset{ID: uuid.NewString(), UserID: owner, TokenHash: sha256Hex("also-mine"), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	someoneElses := domain.PasswordReset{ID: uuid.NewString(), UserID: otherOwner, TokenHash: sha256Hex("someone-elses"), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	for _, reset := range []domain.PasswordReset{mine, alsoMine, someoneElses} {
		if err := repository.CreatePasswordReset(ctx, reset); err != nil {
			t.Fatalf("create password reset: %v", err)
		}
	}

	if err := repository.InvalidatePendingPasswordResets(ctx, owner); err != nil {
		t.Fatalf("invalidate pending password resets: %v", err)
	}

	for _, tokenHash := range []string{mine.TokenHash, alsoMine.TokenHash} {
		fetched, err := repository.GetPasswordResetByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatalf("get password reset: %v", err)
		}
		if fetched.UsedAt == nil {
			t.Fatalf("reset %s should have been invalidated", tokenHash)
		}
	}
	fetched, err := repository.GetPasswordResetByTokenHash(ctx, someoneElses.TokenHash)
	if err != nil {
		t.Fatalf("get password reset: %v", err)
	}
	if fetched.UsedAt != nil {
		t.Fatal("another user's reset must not be invalidated")
	}
}

// TestIdentityRepositoryPasswordResetTokenHashIsUnique confirms the same
// safeguard invitations and refresh tokens have: the database itself refuses
// a second row hashing to the same token, not just the service layer.
func TestIdentityRepositoryPasswordResetTokenHashIsUnique(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	owner := seedUser(t, pool, "password-reset-unique@example.test")
	ctx := context.Background()

	shared := sha256Hex("shared-token")
	first := domain.PasswordReset{ID: uuid.NewString(), UserID: owner, TokenHash: shared, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := repository.CreatePasswordReset(ctx, first); err != nil {
		t.Fatalf("create first password reset: %v", err)
	}
	second := domain.PasswordReset{ID: uuid.NewString(), UserID: owner, TokenHash: shared, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := repository.CreatePasswordReset(ctx, second); err == nil {
		t.Fatal("expected a unique-constraint error for a duplicate token hash")
	}
}
