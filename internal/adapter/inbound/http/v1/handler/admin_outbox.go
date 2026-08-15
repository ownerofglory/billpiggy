package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAdminOutboxRoutes mounts the super-admin dead-letter endpoints.
func RegisterAdminOutboxRoutes(router chi.Router, outboxAdmin inbound.OutboxAdminService, middleware *sharedauth.Middleware) {
	h := adminOutboxHandler{service: outboxAdmin}
	router.Route(basePathV1+"/admin/outbox/dead-letters", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication, permission(middleware, domain.PermissionAuditRead))
		routes.Get("/", h.list)
		routes.Post("/{outboxID}/requeue", h.requeue)
	})
}

type adminOutboxHandler struct{ service inbound.OutboxAdminService }

// list returns outbox deliveries abandoned after exhausting their retries,
// newest first, with the handler error that caused each one.
//
//	@Summary	List abandoned outbox deliveries
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		subscription	query		string	false	"Limit to one subscription; defaults to every subscription"
//	@Param		limit			query		int		false	"Maximum entries to return, 1-200; defaults to 50"
//	@Success	200				{array}		domain.DeadLetter
//	@Failure	400				{object}	map[string]string
//	@Failure	403				{object}	map[string]string
//	@Router		/billpiggy/api/v1/admin/outbox/dead-letters [get]
func (h adminOutboxHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
	letters, err := h.service.ListDeadLetters(r.Context(), actor, r.URL.Query().Get("subscription"), limit)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	writeJSON(w, http.StatusOK, letters)
}

// requeue puts one abandoned delivery back on the queue, which also releases
// the later events for its aggregate that were waiting behind it.
//
//	@Summary	Requeue an abandoned outbox delivery
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		outboxID	path		string	true	"Outbox delivery identifier"
//	@Success	204			{string}	string	"requeued"
//	@Failure	403			{object}	map[string]string
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/admin/outbox/dead-letters/{outboxID}/requeue [post]
func (h adminOutboxHandler) requeue(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
	switch err := h.service.RequeueDeadLetter(r.Context(), actor, chi.URLParam(r, "outboxID")); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, service.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "no abandoned delivery with that id")
	case errors.Is(err, service.ErrForbidden):
		writeJSONError(w, http.StatusForbidden, "permission denied")
	default:
		writeJSONError(w, http.StatusInternalServerError, "could not requeue the delivery")
	}
}
