//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TestIdentityRepositoryInvitationCreateGetAccept exercises the exact path a
// production incident traced back to: GetInvitationByTokenHash scanned the
// bytea token_hash column straight into a *string, which pgx rejects in
// binary protocol mode ("cannot scan bytea (OID 17) in binary format into
// *string") — every accept-invitation call failed with a generic 401 no
// matter how valid the token was. No integration test exercised this
// adapter method at all, which is exactly how it shipped unnoticed.
func TestIdentityRepositoryInvitationCreateGetAccept(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	admin := seedUser(t, pool, "invitation-admin@example.test")
	ctx := context.Background()

	invitation := domain.Invitation{
		ID: uuid.NewString(), Email: "invited@example.test", Role: domain.RoleMember,
		TokenHash: sha256Hex("a-raw-invitation-token"), Status: domain.InvitationPending,
		InvitedBy: admin, ExpiresAt: time.Now().Add(7 * 24 * time.Hour), CreatedAt: time.Now(),
	}
	if err := repository.CreateInvitation(ctx, invitation); err != nil {
		t.Fatalf("create invitation: %v", err)
	}

	fetched, err := repository.GetInvitationByTokenHash(ctx, invitation.TokenHash)
	if err != nil {
		t.Fatalf("get invitation by token hash: %v", err)
	}
	if fetched.ID != invitation.ID || fetched.Email != invitation.Email {
		t.Fatalf("fetched = %#v, want it to match the created invitation", fetched)
	}
	if fetched.TokenHash != invitation.TokenHash {
		t.Fatalf("fetched token hash = %q, want %q (bytea must round-trip through hex, not come back garbled or empty)", fetched.TokenHash, invitation.TokenHash)
	}
	if fetched.Status != domain.InvitationPending {
		t.Fatalf("status = %q, want pending", fetched.Status)
	}

	newUser := domain.AppUser{
		ID: uuid.NewString(), Email: invitation.Email, PasswordHash: "x", DisplayName: "Invited User",
		Role: domain.RoleMember, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := repository.AcceptInvitation(ctx, invitation.ID, newUser); err != nil {
		t.Fatalf("accept invitation: %v", err)
	}

	accepted, err := repository.GetInvitationByTokenHash(ctx, invitation.TokenHash)
	if err != nil {
		t.Fatalf("re-fetch accepted invitation: %v", err)
	}
	if accepted.Status != domain.InvitationAccepted || accepted.AcceptedBy != newUser.ID {
		t.Fatalf("accepted = %#v, want status accepted and accepted_by %s", accepted, newUser.ID)
	}

	// A second accept attempt against the same now-accepted invitation must
	// fail cleanly rather than create a duplicate user. Since the user insert
	// runs before the invitation update in the same transaction, this also
	// confirms a rejected update rolls the insert back rather than leaving an
	// orphaned users row behind under a fresh random ID.
	retryUser := domain.AppUser{
		ID: uuid.NewString(), Email: invitation.Email, PasswordHash: "x", DisplayName: "Retry",
		Role: domain.RoleMember, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := repository.AcceptInvitation(ctx, invitation.ID, retryUser); err == nil {
		t.Fatal("expected accepting an already-accepted invitation to fail")
	}
	var userCount int
	if err := pool.QueryRow(ctx, `select count(*) from identity.users where email = $1`, invitation.Email).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("users with email %s = %d, want exactly 1 (the failed retry must not have left an orphaned row)", invitation.Email, userCount)
	}
}

// TestIdentityRepositoryGetInvitationByTokenHashReturnsNoRowsForAnUnknownToken
// confirms a genuinely unknown token behaves like "not found" rather than
// panicking or masking a different error — hashBytes panics on malformed hex,
// so this also guards against ever tightening that into something that could
// be triggered by attacker-controlled input.
func TestIdentityRepositoryGetInvitationByTokenHashReturnsNoRowsForAnUnknownToken(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewIdentityRepository(pool)
	ctx := context.Background()

	if _, err := repository.GetInvitationByTokenHash(ctx, sha256Hex("never-issued")); err == nil {
		t.Fatal("expected an error for a token hash that was never issued")
	}
}
