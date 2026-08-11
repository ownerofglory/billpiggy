package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterExpenseRoutes mounts authenticated expense CRUD endpoints.
func RegisterExpenseRoutes(router chi.Router, expenses inbound.ExpenseService, middleware *sharedauth.Middleware) {
	h := expenseHandler{service: expenses}
	router.Route(basePathV1+"/expenses", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Post("/", h.create)
		routes.Get("/{expenseID}", h.get)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Put("/{expenseID}", h.update)
		routes.With(permission(middleware, domain.PermissionExpensesDelete)).Delete("/{expenseID}", h.delete)
	})
}
func permission(m *sharedauth.Middleware, p domain.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return m.RequirePermission(string(p), next) }
}

type expenseHandler struct{ service inbound.ExpenseService }
type expenseRequest struct {
	Title         string               `json:"title"`
	AmountMinor   int64                `json:"amount_minor"`
	Currency      string               `json:"currency"`
	OccurredAt    time.Time            `json:"occurred_at"`
	CategoryID    string               `json:"category_id"`
	CategoryName  string               `json:"category_name"`
	TagIDs        []string             `json:"tag_ids"`
	Status        domain.ExpenseStatus `json:"status"`
	SharedGroupID string               `json:"shared_group_id"`
	Items         []domain.ExpenseItem `json:"items"`
	Latitude      *float64             `json:"latitude"`
	Longitude     *float64             `json:"longitude"`
	Address       string               `json:"address"`
}

func (h expenseHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

func (h expenseHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}
func (h expenseHandler) command(request expenseRequest) service.CreateExpenseCommand {
	return service.CreateExpenseCommand{Title: request.Title, AmountMinor: request.AmountMinor, Currency: request.Currency, OccurredAt: request.OccurredAt, CategoryID: request.CategoryID, CategoryName: request.CategoryName, TagIDs: request.TagIDs, Status: request.Status, SharedGroupID: request.SharedGroupID, Items: request.Items, Latitude: request.Latitude, Longitude: request.Longitude, Address: request.Address}
}

// list returns the current user's most recent expenses.
//
//	@Summary	List expenses
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		q			query	string		false	"Search title or category"
//	@Param		category_id	query	string		false	"Category ID"
//	@Param		tag_id		query	[]string	false	"Tag IDs"
//	@Success	200			{array}	domain.ExpenseRecord
//	@Router		/billpiggy/api/v1/expenses/ [get]
func (h expenseHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))
	values, err := h.service.ListExpensesForViewer(r.Context(), h.actor(r), outbound.ExpenseListFilter{Query: query.Get("q"), CategoryID: query.Get("category_id"), TagIDs: query["tag_id"], Limit: limit, Offset: offset})
	if err != nil {
		writeJSONError(w, 500, "could not list expenses")
		return
	}
	writeJSON(w, 200, values)
}

// get returns one expense and its optional receipt details.
//
//	@Summary	Get expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Param		expenseID	path		string	true	"Expense ID"
//	@Success	200			{object}	domain.ExpenseRecord
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/expenses/{expenseID} [get]
func (h expenseHandler) get(w http.ResponseWriter, r *http.Request) {
	value, err := h.service.GetExpenseForViewer(r.Context(), h.actor(r), chi.URLParam(r, "expenseID"))
	if err != nil {
		writeJSONError(w, 404, "expense not found")
		return
	}
	writeJSON(w, 200, value)
}

// create records a new expense.
//
//	@Summary	Create expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		expenseRequest	true	"Expense"
//	@Success	201		{object}	domain.ExpenseRecord
//	@Router		/billpiggy/api/v1/expenses/ [post]
func (h expenseHandler) create(w http.ResponseWriter, r *http.Request) {
	var request expenseRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := h.service.CreateExpense(r.Context(), h.owner(r), h.command(request))
	if err != nil {
		writeJSONError(w, 400, "invalid expense")
		return
	}
	writeJSON(w, 201, value)
}

// update edits an existing expense.
//
//	@Summary	Update expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		expenseID	path		string			true	"Expense ID"
//	@Param		request		body		expenseRequest	true	"Expense"
//	@Success	200			{object}	domain.ExpenseRecord
//	@Router		/billpiggy/api/v1/expenses/{expenseID} [put]
func (h expenseHandler) update(w http.ResponseWriter, r *http.Request) {
	var request expenseRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := h.service.UpdateExpense(r.Context(), h.owner(r), chi.URLParam(r, "expenseID"), h.command(request))
	if err != nil {
		writeJSONError(w, 400, "expense could not be updated")
		return
	}
	writeJSON(w, 200, value)
}

// delete removes an expense from normal read models.
//
//	@Summary	Delete expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Param		expenseID	path	string	true	"Expense ID"
//	@Success	204
//	@Router		/billpiggy/api/v1/expenses/{expenseID} [delete]
func (h expenseHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteExpense(r.Context(), h.owner(r), chi.URLParam(r, "expenseID")); err != nil {
		writeJSONError(w, 404, "expense not found")
		return
	}
	w.WriteHeader(204)
}

var _ = strings.TrimSpace
