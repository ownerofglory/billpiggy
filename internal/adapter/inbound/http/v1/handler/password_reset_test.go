package handler_test

import (
	"net/http"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TestE2EPasswordResetRequestConfirmLoginFlow drives the full requirement
// through the real router: request a reset, pull the token out of the
// queued email exactly as a mail client would show it, confirm it, and
// verify the old password stops working while the new one logs in.
func TestE2EPasswordResetRequestConfirmLoginFlow(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	memberToken := app.inviteAndLogin(t, adminToken, "member@example.com", "member", "member-password-1")
	_ = memberToken

	requestResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset", "", map[string]string{"email": "member@example.com"}, nil)
	if requestResponse.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, want 202", requestResponse.Code)
	}

	var token string
	for _, delivery := range app.notifications.Deliveries() {
		if delivery.Kind == domain.NotificationPasswordReset {
			token = delivery.Payload["token"]
		}
	}
	if token == "" {
		t.Fatal("no password_reset email was queued")
	}

	confirmResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset/confirm", "", map[string]string{
		"token": token, "new_password": "a-brand-new-password",
	}, nil)
	if confirmResponse.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, body = %s", confirmResponse.Code, confirmResponse.Body.String())
	}

	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": "member@example.com", "password": "member-password-1"}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password status = %d, want 401", response.Code)
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": "member@example.com", "password": "a-brand-new-password"}, &session); response.Code != http.StatusOK {
		t.Fatalf("login with new password status = %d, body = %s", response.Code, response.Body.String())
	}
	if session.AccessToken == "" {
		t.Fatal("login did not return an access token")
	}

	// The token cannot be replayed.
	replayResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset/confirm", "", map[string]string{
		"token": token, "new_password": "yet-another-password",
	}, nil)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", replayResponse.Code)
	}
}

// TestE2EPasswordResetRequestDoesNotRevealAccountExistence checks the
// observable HTTP behaviour of the property the service guarantees: a
// known and an unknown email must get byte-identical responses.
func TestE2EPasswordResetRequestDoesNotRevealAccountExistence(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	app.inviteAndLogin(t, adminToken, "known@example.com", "member", "known-password-1")

	knownResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset", "", map[string]string{"email": "known@example.com"}, nil)
	unknownResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset", "", map[string]string{"email": "no-such-account@example.com"}, nil)

	if knownResponse.Code != http.StatusAccepted || unknownResponse.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d / %d, want 202 / 202", knownResponse.Code, unknownResponse.Code)
	}
	if knownResponse.Body.String() != unknownResponse.Body.String() {
		t.Fatalf("response bodies differ: %q vs %q", knownResponse.Body.String(), unknownResponse.Body.String())
	}
}

func TestE2EPasswordResetConfirmRejectsAnInvalidToken(t *testing.T) {
	app := newE2EApp(t, "")
	response := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset/confirm", "", map[string]string{
		"token": "not-a-real-token", "new_password": "a-brand-new-password",
	}, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestE2EPasswordResetConfirmRejectsAShortPassword(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	app.inviteAndLogin(t, adminToken, "shortpw@example.com", "member", "shortpw-password-1")

	app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset", "", map[string]string{"email": "shortpw@example.com"}, nil)
	var token string
	for _, delivery := range app.notifications.Deliveries() {
		if delivery.Kind == domain.NotificationPasswordReset {
			token = delivery.Payload["token"]
		}
	}
	if token == "" {
		t.Fatal("no password_reset email was queued")
	}
	response := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/password-reset/confirm", "", map[string]string{
		"token": token, "new_password": "short",
	}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
