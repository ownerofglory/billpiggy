// Package auth provides reusable HTTP authentication and authorization primitives.
package auth

import (
	"context"
	"crypto/subtle"
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

// RequireBearerToken protects an endpoint with one static, pre-shared bearer
// token, for a machine caller — a Prometheus scraper is the motivating case —
// that cannot participate in the interactive login/refresh flow
// RequireAuthentication expects. An empty want always rejects: without that,
// an operator who forgets to configure the token would silently leave the
// endpoint open rather than the fail-closed default this exists to provide.
// The comparison is constant-time so response timing can't be used to guess
// the token a byte at a time.
func RequireBearerToken(want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok || want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
