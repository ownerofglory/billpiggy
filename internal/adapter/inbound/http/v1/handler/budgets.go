package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterBudgetRoutes mounts authenticated budget CRUD endpoints.
func RegisterBudgetRoutes(router chi.Router, budgets *service.BudgetService, middleware *sharedauth.Middleware) {
	h := budgetHandler{service: budgets}
	router.Route(basePathV1+"/budgets", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.With(permission(middleware, domain.PermissionBudgetsRead)).Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionBudgetsWrite)).Post("/", h.create)
		routes.With(permission(middleware, domain.PermissionBudgetsRead)).Get("/{budgetID}", h.get)
		routes.With(permission(middleware, domain.PermissionBudgetsWrite)).Put("/{budgetID}", h.update)
		routes.With(permission(middleware, domain.PermissionBudgetsWrite)).Delete("/{budgetID}", h.delete)
	})
}

type budgetHandler struct{ service *service.BudgetService }

type budgetRequest struct {
	Name             string              `json:"name"`
	CategoryID       string              `json:"category_id"`
	AmountLimitMinor int64               `json:"amount_limit_minor"`
	Currency         string              `json:"currency"`
	ThresholdPercent int                 `json:"threshold_percent"`
	Period           domain.BudgetPeriod `json:"period"`
	DueAt            *time.Time          `json:"due_at"`
	SharedGroupID    string              `json:"shared_group_id"`
}

func (h budgetHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}

func (h budgetHandler) budget(request budgetRequest) domain.BudgetRecord {
	return domain.BudgetRecord{Name: request.Name, CategoryID: request.CategoryID, AmountLimitMinor: request.AmountLimitMinor, Currency: request.Currency, ThresholdPercent: request.ThresholdPercent, Period: request.Period, DueAt: request.DueAt, SharedGroupID: request.SharedGroupID}
}

// list returns the current user's budgets.
//
//	@Summary	List budgets
//	@Tags		budgets
//	@Produce	json
//	@Success	200	{array}		domain.BudgetRecord
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/budgets/ [get]
func (h budgetHandler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListBudgets(r.Context(), h.actor(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not list budgets")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// get returns a current user's budget.
//
//	@Summary	Get budget
//	@Tags		budgets
//	@Produce	json
//	@Param		budgetID	path		string	true	"Budget ID"
//	@Success	200			{object}	domain.BudgetRecord
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/budgets/{budgetID} [get]
func (h budgetHandler) get(w http.ResponseWriter, r *http.Request) {
	budget, err := h.service.GetBudget(r.Context(), h.actor(r), chi.URLParam(r, "budgetID"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "budget not found")
		return
	}
	writeJSON(w, http.StatusOK, budget)
}

// create creates a new category budget.
//
//	@Summary	Create budget
//	@Tags		budgets
//	@Accept		json
//	@Produce	json
//	@Param		request	body		budgetRequest	true	"Budget"
//	@Success	201		{object}	domain.BudgetRecord
//	@Failure	400		{object}	map[string]string
//	@Router		/billpiggy/api/v1/budgets/ [post]
func (h budgetHandler) create(w http.ResponseWriter, r *http.Request) {
	var request budgetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	budget, err := h.service.CreateBudget(r.Context(), h.actor(r), h.budget(request))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid budget")
		return
	}
	writeJSON(w, http.StatusCreated, budget)
}

// update replaces an existing budget owned by the current user.
//
//	@Summary	Update budget
//	@Tags		budgets
//	@Accept		json
//	@Produce	json
//	@Param		budgetID	path		string			true	"Budget ID"
//	@Param		request		body		budgetRequest	true	"Budget"
//	@Success	200			{object}	domain.BudgetRecord
//	@Failure	400			{object}	map[string]string
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/budgets/{budgetID} [put]
func (h budgetHandler) update(w http.ResponseWriter, r *http.Request) {
	var request budgetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	budget, err := h.service.UpdateBudget(r.Context(), h.actor(r), chi.URLParam(r, "budgetID"), h.budget(request))
	if errors.Is(err, service.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "budget not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid budget")
		return
	}
	writeJSON(w, http.StatusOK, budget)
}

// delete soft-deletes a budget owned by the current user.
//
//	@Summary	Delete budget
//	@Tags		budgets
//	@Param		budgetID	path	string	true	"Budget ID"
//	@Success	204
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/budgets/{budgetID} [delete]
func (h budgetHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteBudget(r.Context(), h.actor(r), chi.URLParam(r, "budgetID")); err != nil {
		writeJSONError(w, http.StatusNotFound, "budget not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
