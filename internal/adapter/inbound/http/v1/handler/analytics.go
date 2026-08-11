package handler

import (
	"net/http"
	"net/url"
	"strconv"
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
		routes.Get("/comparison", h.comparison)
		routes.Get("/category-changes", h.categoryChanges)
		routes.Get("/burn-rate", h.burnRate)
		routes.Get("/daily-totals", h.dailyTotals)
		routes.Get("/weekday-breakdown", h.weekdayBreakdown)
		routes.Get("/top-expenses", h.topExpenses)
		routes.Get("/budget-progress", h.budgetProgress)
	})
}

// listSuggestions returns budget threshold recommendations for the current month.
//
//	@Summary	Get budget suggestions
//	@Tags		analytics
//	@Security	ApiKeyAuth
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

// listExpenses returns category or tag spending grouped by a requested
// period. Calling it with no category_id over a week/month/year range also
// serves a category-over-time view: one row per (category, period bucket).
//
//	@Summary	Get expense analytics
//	@Tags		analytics
//	@Security	ApiKeyAuth
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
	from, to, ok := parseDateRange(query)
	if !ok {
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

// comparison compares the current period bucket against the immediately
// preceding one, e.g. this week vs last week.
//
//	@Summary	Compare the current period against the previous one
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		period	query		string	true	"day, week, month, or year"
//	@Success	200		{object}	domain.PeriodComparison
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/comparison [get]
func (h analyticsHandler) comparison(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	period := domain.AnalyticsPeriod(r.URL.Query().Get("period"))
	value, err := h.service.ComparePeriods(r.Context(), identity.Subject, period)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// categoryChanges ranks categories by current-period spend alongside their
// spend in the immediately preceding period.
//
//	@Summary	Rank categories by spend change against the previous period
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		period	query		string	true	"day, week, month, or year"
//	@Param		limit	query		int		false	"Max categories to return (default 10, max 50)"
//	@Success	200		{array}		domain.CategoryChange
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/category-changes [get]
func (h analyticsHandler) categoryChanges(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	query := r.URL.Query()
	period := domain.AnalyticsPeriod(query.Get("period"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	values, err := h.service.TopCategoryChanges(r.Context(), identity.Subject, period, limit)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// burnRate reports spend so far in the current period, projected forward at
// the observed daily average, against any matching-period budgets.
//
//	@Summary	Get spend-so-far, projected total, and budget target for the current period
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		period	query		string	true	"day, week, month, or year"
//	@Success	200		{array}		domain.BurnRate
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/burn-rate [get]
func (h analyticsHandler) burnRate(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	period := domain.AnalyticsPeriod(r.URL.Query().Get("period"))
	values, err := h.service.BurnRate(r.Context(), identity.Subject, period)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// dailyTotals sums spend across all categories onto each calendar day, for a
// calendar-heatmap style view.
//
//	@Summary	Get one spend total per calendar day
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		from	query		string	true	"RFC3339 inclusive start"
//	@Param		to		query		string	true	"RFC3339 inclusive end"
//	@Success	200		{array}		domain.DailyTotal
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/daily-totals [get]
func (h analyticsHandler) dailyTotals(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	from, to, ok := parseDateRange(r.URL.Query())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from and to must be ordered RFC3339 timestamps")
		return
	}
	values, err := h.service.DailyTotals(r.Context(), identity.Subject, from, to)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// weekdayBreakdown sums spend across all categories onto each weekday within
// the requested range.
//
//	@Summary	Get one spend total per weekday
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		from	query		string	true	"RFC3339 inclusive start"
//	@Param		to		query		string	true	"RFC3339 inclusive end"
//	@Success	200		{array}		domain.WeekdayTotal
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/weekday-breakdown [get]
func (h analyticsHandler) weekdayBreakdown(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	from, to, ok := parseDateRange(r.URL.Query())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from and to must be ordered RFC3339 timestamps")
		return
	}
	values, err := h.service.WeekdayBreakdown(r.Context(), identity.Subject, from, to)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// topExpenses returns the largest individual expenses in the requested range.
//
//	@Summary	Get the largest individual expenses in a range
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		from	query		string	true	"RFC3339 inclusive start"
//	@Param		to		query		string	true	"RFC3339 inclusive end"
//	@Param		limit	query		int		false	"Max expenses to return (default 10, max 50)"
//	@Success	200		{array}		domain.ExpenseRecord
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/top-expenses [get]
func (h analyticsHandler) topExpenses(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	query := r.URL.Query()
	from, to, ok := parseDateRange(query)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from and to must be ordered RFC3339 timestamps")
		return
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	values, err := h.service.TopExpenses(r.Context(), identity.Subject, from, to, limit)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics query")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// budgetProgress reports every one of the current user's budgets against
// spend for its own current period window.
//
//	@Summary	Get every budget's spend against its limit for its current period
//	@Tags		analytics
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Success	200	{array}		domain.BudgetProgress
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/analytics/budget-progress [get]
func (h analyticsHandler) budgetProgress(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	values, err := h.service.BudgetProgress(r.Context(), identity.Subject)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load budget progress")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// parseDateRange parses the from/to RFC3339 query params shared by every
// range-based analytics endpoint, reporting false when either is missing,
// malformed, or out of order.
func parseDateRange(query url.Values) (time.Time, time.Time, bool) {
	from, fromErr := time.Parse(time.RFC3339, query.Get("from"))
	to, toErr := time.Parse(time.RFC3339, query.Get("to"))
	if fromErr != nil || toErr != nil || to.Before(from) {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}
