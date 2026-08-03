package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterGroupRoutes mounts endpoints for private administrator-managed groups.
func RegisterGroupRoutes(router chi.Router, groups *service.GroupService, middleware *sharedauth.Middleware) {
	h := groupHandler{service: groups}
	router.Route(basePathV1+"/groups", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Post("/", h.create)
	})
}

type groupHandler struct{ service *service.GroupService }

type createGroupRequest struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}

// list returns the authenticated user's visible groups.
//
//	@Summary	List visible groups
//	@Tags		groups
//	@Produce	json
//	@Success	200	{array}		domain.UserGroup
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/ [get]
func (h groupHandler) list(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	groups, err := h.service.ListVisibleGroups(r.Context(), domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not list groups")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// create makes a private group owned by the authenticated administrator.
//
//	@Summary	Create group
//	@Tags		groups, administration
//	@Accept		json
//	@Produce	json
//	@Param		request	body		createGroupRequest	true	"Group"
//	@Success	201		{object}	domain.UserGroup
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/ [post]
func (h groupHandler) create(w http.ResponseWriter, r *http.Request) {
	var request createGroupRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	group, err := h.service.CreateGroup(r.Context(), domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}, request.Name, request.MemberIDs)
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid group")
		return
	}
	writeJSON(w, http.StatusCreated, group)
}
