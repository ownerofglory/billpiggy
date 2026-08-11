package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAuditRoutes mounts the super-admin audit trail query endpoint.
func RegisterAuditRoutes(router chi.Router, audit inbound.AuditService, middleware *sharedauth.Middleware) {
	h := auditHandler{service: audit}
	router.Route(basePathV1+"/audit", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication, permission(middleware, domain.PermissionAuditRead))
		routes.Get("/", h.list)
	})
}

type auditHandler struct{ service inbound.AuditService }

// list returns audit entries matching the request's filters.
//
//	@Summary	List audit entries
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		actor_id		query		string	false	"Actor user ID"
//	@Param		resource_type	query		string	false	"Aggregate type, e.g. expense, budget, user"
//	@Param		resource_id		query		string	false	"Aggregate ID"
//	@Param		action			query		string	false	"Event type, e.g. expense_added"
//	@Param		from			query		string	false	"RFC3339 inclusive start"
//	@Param		to				query		string	false	"RFC3339 exclusive end"
//	@Param		limit			query		int		false	"Page size, default 50, max 200"
//	@Param		offset			query		int		false	"Page offset"
//	@Success	200				{array}		domain.AuditEntry
//	@Failure	400				{object}	map[string]string
//	@Failure	403				{object}	map[string]string
//	@Router		/billpiggy/api/v1/audit [get]
func (h auditHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := outbound.AuditFilter{
		ActorID: query.Get("actor_id"), ResourceType: query.Get("resource_type"),
		ResourceID: query.Get("resource_id"), Action: query.Get("action"),
	}
	if value := query.Get("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "from must be an RFC3339 timestamp")
			return
		}
		filter.From = parsed
	}
	if value := query.Get("to"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "to must be an RFC3339 timestamp")
			return
		}
		filter.To = parsed
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		filter.Limit = limit
	}
	if value := query.Get("offset"); value != "" {
		offset, err := strconv.Atoi(value)
		if err != nil || offset < 0 {
			writeJSONError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		filter.Offset = offset
	}

	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
	entries, err := h.service.ListEntries(r.Context(), actor, filter)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
