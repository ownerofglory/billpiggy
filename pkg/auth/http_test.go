package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/auth"
)

func TestMiddlewareAuthenticationAndAuthorization(t *testing.T) {
	t.Parallel()
	middleware := auth.NewMiddleware(testAuthenticator{}, testAuthorizer{})
	handler := middleware.RequireAuthentication(middleware.RequirePermission("expenses:read", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok || identity.Subject != "user-1" {
			t.Fatal("identity missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	for _, test := range []struct {
		name, authorization string
		wantStatus          int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "invalid", authorization: "Bearer invalid", wantStatus: http.StatusUnauthorized},
		{name: "forbidden", authorization: "Bearer member", wantStatus: http.StatusForbidden},
		{name: "allowed", authorization: "Bearer admin", wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

type testAuthenticator struct{}

func (testAuthenticator) Authenticate(_ context.Context, token string) (auth.Identity, error) {
	switch token {
	case "admin":
		return auth.Identity{Subject: "user-1", Role: "admin"}, nil
	case "member":
		return auth.Identity{Subject: "user-2", Role: "member"}, nil
	default:
		return auth.Identity{}, errors.New("invalid token")
	}
}

type testAuthorizer struct{}

func (testAuthorizer) Allows(identity auth.Identity, permission string) bool {
	return identity.Role == "admin" && permission == "expenses:read"
}
