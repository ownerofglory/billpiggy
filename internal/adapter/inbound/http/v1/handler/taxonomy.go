package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterTaxonomyRoutes mounts personal category and tag endpoints.
func RegisterTaxonomyRoutes(router chi.Router, taxonomy inbound.TaxonomyService, middleware *sharedauth.Middleware) {
	h := taxonomyHandler{service: taxonomy}
	router.Route(basePathV1+"/taxonomy", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/categories", h.listCategories)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Post("/categories", h.createCategory)
		routes.Get("/tags", h.listTags)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Post("/tags", h.createTag)
	})
}

type taxonomyHandler struct{ service inbound.TaxonomyService }
type taxonomyRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (h taxonomyHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

// listCategories returns system defaults and the current user's categories.
//
//	@Summary	List categories
//	@Tags		taxonomy
//	@Produce	json
//	@Success	200	{array}	domain.ExpenseCategory
//	@Router		/billpiggy/api/v1/taxonomy/categories [get]
func (h taxonomyHandler) listCategories(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListCategories(r.Context(), h.owner(r))
	if err != nil {
		writeJSONError(w, 500, "could not list categories")
		return
	}
	writeJSON(w, 200, values)
}

// createCategory adds a category for the current user.
//
//	@Summary	Create category
//	@Tags		taxonomy
//	@Accept		json
//	@Produce	json
//	@Param		request	body		taxonomyRequest	true	"Category"
//	@Success	201		{object}	domain.ExpenseCategory
//	@Router		/billpiggy/api/v1/taxonomy/categories [post]
func (h taxonomyHandler) createCategory(w http.ResponseWriter, r *http.Request) {
	var req taxonomyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	value, err := h.service.CreateCategory(r.Context(), h.owner(r), req.Name, req.Color)
	if err != nil {
		writeJSONError(w, 400, "invalid category")
		return
	}
	writeJSON(w, 201, value)
}

// listTags returns the current user's tags.
//
//	@Summary	List tags
//	@Tags		taxonomy
//	@Produce	json
//	@Success	200	{array}	domain.ExpenseTag
//	@Router		/billpiggy/api/v1/taxonomy/tags [get]
func (h taxonomyHandler) listTags(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListTags(r.Context(), h.owner(r))
	if err != nil {
		writeJSONError(w, 500, "could not list tags")
		return
	}
	writeJSON(w, 200, values)
}

// createTag adds a tag for the current user.
//
//	@Summary	Create tag
//	@Tags		taxonomy
//	@Accept		json
//	@Produce	json
//	@Param		request	body		taxonomyRequest	true	"Tag"
//	@Success	201		{object}	domain.ExpenseTag
//	@Router		/billpiggy/api/v1/taxonomy/tags [post]
func (h taxonomyHandler) createTag(w http.ResponseWriter, r *http.Request) {
	var req taxonomyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	value, err := h.service.CreateTag(r.Context(), h.owner(r), req.Name, req.Color)
	if err != nil {
		writeJSONError(w, 400, "invalid tag")
		return
	}
	writeJSON(w, 201, value)
}
