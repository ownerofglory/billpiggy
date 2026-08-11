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
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Put("/categories/{categoryID}", h.updateCategory)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Delete("/categories/{categoryID}", h.deleteCategory)
		routes.Get("/tags", h.listTags)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Post("/tags", h.createTag)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Put("/tags/{tagID}", h.updateTag)
		routes.With(permission(middleware, domain.PermissionExpensesWrite)).Delete("/tags/{tagID}", h.deleteTag)
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
//	@Security	ApiKeyAuth
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
//	@Security	ApiKeyAuth
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

// updateCategory renames or recolors a personal category.
//
//	@Summary	Update category
//	@Tags		taxonomy
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		categoryID	path		string			true	"Category ID"
//	@Param		request		body		taxonomyRequest	true	"Category"
//	@Success	200			{object}	domain.ExpenseCategory
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/taxonomy/categories/{categoryID} [put]
func (h taxonomyHandler) updateCategory(w http.ResponseWriter, r *http.Request) {
	var req taxonomyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	value, err := h.service.UpdateCategory(r.Context(), h.owner(r), chi.URLParam(r, "categoryID"), req.Name, req.Color)
	if err != nil {
		writeJSONError(w, 404, "category not found")
		return
	}
	writeJSON(w, 200, value)
}

// deleteCategory removes a personal category.
//
//	@Summary	Delete category
//	@Tags		taxonomy
//	@Security	ApiKeyAuth
//	@Param		categoryID	path	string	true	"Category ID"
//	@Success	204
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/taxonomy/categories/{categoryID} [delete]
func (h taxonomyHandler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteCategory(r.Context(), h.owner(r), chi.URLParam(r, "categoryID")); err != nil {
		writeJSONError(w, 404, "category not found")
		return
	}
	w.WriteHeader(204)
}

// listTags returns the current user's tags.
//
//	@Summary	List tags
//	@Tags		taxonomy
//	@Security	ApiKeyAuth
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
//	@Security	ApiKeyAuth
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

// updateTag renames or recolors a personal tag.
//
//	@Summary	Update tag
//	@Tags		taxonomy
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		tagID	path		string			true	"Tag ID"
//	@Param		request	body		taxonomyRequest	true	"Tag"
//	@Success	200		{object}	domain.ExpenseTag
//	@Failure	404		{object}	map[string]string
//	@Router		/billpiggy/api/v1/taxonomy/tags/{tagID} [put]
func (h taxonomyHandler) updateTag(w http.ResponseWriter, r *http.Request) {
	var req taxonomyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	value, err := h.service.UpdateTag(r.Context(), h.owner(r), chi.URLParam(r, "tagID"), req.Name, req.Color)
	if err != nil {
		writeJSONError(w, 404, "tag not found")
		return
	}
	writeJSON(w, 200, value)
}

// deleteTag removes a personal tag.
//
//	@Summary	Delete tag
//	@Tags		taxonomy
//	@Security	ApiKeyAuth
//	@Param		tagID	path	string	true	"Tag ID"
//	@Success	204
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/taxonomy/tags/{tagID} [delete]
func (h taxonomyHandler) deleteTag(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteTag(r.Context(), h.owner(r), chi.URLParam(r, "tagID")); err != nil {
		writeJSONError(w, 404, "tag not found")
		return
	}
	w.WriteHeader(204)
}
