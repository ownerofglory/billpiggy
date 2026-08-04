package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAnalyticsRoutes mounts authenticated analytics read endpoints.
func RegisterAnalyticsRoutes(router chi.Router, analytics inbound.AnalyticsService, middleware *sharedauth.Middleware) {
	h := analyticsHandler{service: analytics}
	router.Route(basePathV1+"/analytics", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication, permission(middleware, domain.PermissionAnalyticsRead))
		routes.Get("/expenses", h.listExpenses)
		routes.Get("/suggestions", h.listSuggestions)
	})
}

// listSuggestions returns budget threshold recommendations for the current month.
//
//	@Summary	Get budget suggestions
//	@Tags		analytics
//	@Produce	json
//	@Success	200	{array}		domain.BudgetSuggestion
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/suggestions [get]
func (h analyticsHandler) listSuggestions(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	values, err := h.service.ListBudgetSuggestions(r.Context(), identity.Subject)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not create suggestions")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

type analyticsHandler struct{ service inbound.AnalyticsService }

// listExpenses returns category or tag spending grouped by a requested period.
//
//	@Summary	Get expense analytics
//	@Tags		analytics
//	@Produce	json
//	@Param		period		query		string		true	"day, week, month, or year"
//	@Param		from		query		string		true	"RFC3339 inclusive start"
//	@Param		to			query		string		true	"RFC3339 inclusive end"
//	@Param		category_id	query		string		false	"Category ID"
//	@Param		tag_id		query		[]string	false	"Tag IDs"
//	@Success	200			{array}		domain.ExpenseRollup
//	@Failure	400			{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/expenses [get]
func (h analyticsHandler) listExpenses(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, fromErr := time.Parse(time.RFC3339, query.Get("from"))
	to, toErr := time.Parse(time.RFC3339, query.Get("to"))
	if fromErr != nil || toErr != nil || to.Before(from) {
		writeJSONError(w, http.StatusBadRequest, "from and to must be ordered RFC3339 timestamps")
		return
	}
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	values, err := h.service.ListExpenseRollups(r.Context(), outbound.AnalyticsFilter{OwnerID: identity.Subject, Period: domain.AnalyticsPeriod(query.Get("period")), From: from, To: to, CategoryID: query.Get("category_id"), TagIDs: query["tag_id"]})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}
