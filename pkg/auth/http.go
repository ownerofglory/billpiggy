// Package auth provides reusable HTTP authentication and authorization primitives.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Identity is the authenticated principal made available to downstream handlers.
type Identity struct {
	Subject string
	Email   string
	Role    string
}

// Authenticator validates a bearer token and resolves the current principal.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Identity, error)
}

// Authorizer checks whether a principal may perform a resource:action permission.
type Authorizer interface {
	Allows(identity Identity, permission string) bool
}

// Middleware creates authentication and permission middleware from small, portable interfaces.
type Middleware struct {
	authenticator Authenticator
	authorizer    Authorizer
}

// NewMiddleware creates a reusable HTTP middleware set.
func NewMiddleware(authenticator Authenticator, authorizer Authorizer) *Middleware {
	return &Middleware{authenticator: authenticator, authorizer: authorizer}
}

// RequireAuthentication rejects requests that lack a valid bearer token.
func (m *Middleware) RequireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.authenticator == nil {
			writeError(w, http.StatusInternalServerError, "authentication is not configured")
			return
		}
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		identity, err := m.authenticator.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
	})
}

// RequirePermission must be used after RequireAuthentication.
func (m *Middleware) RequirePermission(permission string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if m.authorizer == nil || !m.authorizer.Allows(identity, permission) {
			writeError(w, http.StatusForbidden, "permission denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IdentityFromContext returns the authenticated principal when middleware has run.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

type identityKey struct{}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
