// Package handler contains HTTP handlers exposed by version 1 of the public API.
package handler

import (
	"net/http"

	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

const (
	basePathV1 = "/billpiggy/api/v1"
)

// requireAIEnabled reports whether the authenticated user has opted into AI
// features, writing a 403 and returning false otherwise. Every AI-backed
// endpoint (the assistant chat, receipt extraction, sentence and dictation
// expense entry) calls this before spending a provider request.
func requireAIEnabled(w http.ResponseWriter, r *http.Request, auth inbound.AuthService) bool {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	user, err := auth.GetProfile(r.Context(), identity.Subject)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return false
	}
	if !user.AIEnabled {
		writeJSONError(w, http.StatusForbidden, "AI features are disabled for this account")
		return false
	}
	return true
}
