package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAdminUsageRoutes mounts the super-admin usage summary endpoint.
func RegisterAdminUsageRoutes(router chi.Router, usage inbound.AdminUsageService, middleware *sharedauth.Middleware) {
	h := adminUsageHandler{service: usage}
	router.Route(basePathV1+"/admin/usage", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication, permission(middleware, domain.PermissionAuditRead))
		routes.Get("/", h.summarize)
	})
}

type adminUsageHandler struct{ service inbound.AdminUsageService }

// summarize returns account, AI usage, and notification activity since a
// requested time.
//
//	@Summary	Get super-admin usage summary
//	@Tags		administration
//	@Produce	json
//	@Param		since	query		string	false	"RFC3339 inclusive start; defaults to the last 24 hours"
//	@Success	200		{object}	service.UsageSummary
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Router		/billpiggy/api/v1/admin/usage [get]
func (h adminUsageHandler) summarize(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if value := r.URL.Query().Get("since"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
		since = parsed
	}
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
	summary, err := h.service.Summarize(r.Context(), actor, since)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
