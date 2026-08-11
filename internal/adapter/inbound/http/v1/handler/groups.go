package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterGroupRoutes mounts endpoints for private administrator-managed groups.
func RegisterGroupRoutes(router chi.Router, groups inbound.GroupService, middleware *sharedauth.Middleware) {
	h := groupHandler{service: groups}
	router.Route(basePathV1+"/groups", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Post("/", h.create)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Put("/{groupID}", h.update)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Delete("/{groupID}", h.delete)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Post("/{groupID}/members/{userID}", h.addMember)
		routes.With(permission(middleware, domain.PermissionGroupsManage)).Delete("/{groupID}/members/{userID}", h.removeMember)
	})
}

type groupHandler struct{ service inbound.GroupService }

type createGroupRequest struct {
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}

type updateGroupRequest struct {
	Name string `json:"name"`
}

func (h groupHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}

// list returns the authenticated user's visible groups.
//
//	@Summary	List visible groups
//	@Tags		groups
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Success	200	{array}		domain.UserGroup
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/ [get]
func (h groupHandler) list(w http.ResponseWriter, r *http.Request) {
	groups, err := h.service.ListVisibleGroups(r.Context(), h.actor(r))
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
//	@Security	ApiKeyAuth
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
	group, err := h.service.CreateGroup(r.Context(), h.actor(r), request.Name, request.MemberIDs)
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

// update renames a group. Restricted to the group's creator or a super-admin.
//
//	@Summary	Update group
//	@Tags		groups, administration
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		groupID	path		string				true	"Group ID"
//	@Param		request	body		updateGroupRequest	true	"Group"
//	@Success	200		{object}	domain.UserGroup
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/{groupID} [put]
func (h groupHandler) update(w http.ResponseWriter, r *http.Request) {
	var request updateGroupRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	group, err := h.service.UpdateGroup(r.Context(), h.actor(r), chi.URLParam(r, "groupID"), request.Name)
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "group not found")
		return
	}
	writeJSON(w, http.StatusOK, group)
}

// delete removes a group. Restricted to the group's creator or a super-admin.
//
//	@Summary	Delete group
//	@Tags		groups, administration
//	@Security	ApiKeyAuth
//	@Param		groupID	path	string	true	"Group ID"
//	@Success	204
//	@Failure	403	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/{groupID} [delete]
func (h groupHandler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.service.DeleteGroup(r.Context(), h.actor(r), chi.URLParam(r, "groupID"))
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "group not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addMember adds a member to a group. Restricted to the group's creator or a
// super-admin.
//
//	@Summary	Add group member
//	@Tags		groups, administration
//	@Security	ApiKeyAuth
//	@Param		groupID	path	string	true	"Group ID"
//	@Param		userID	path	string	true	"User ID"
//	@Success	204
//	@Failure	403	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/{groupID}/members/{userID} [post]
func (h groupHandler) addMember(w http.ResponseWriter, r *http.Request) {
	err := h.service.AddMember(r.Context(), h.actor(r), chi.URLParam(r, "groupID"), chi.URLParam(r, "userID"))
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "group not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeMember removes a member from a group. Restricted to the group's
// creator or a super-admin.
//
//	@Summary	Remove group member
//	@Tags		groups, administration
//	@Security	ApiKeyAuth
//	@Param		groupID	path	string	true	"Group ID"
//	@Param		userID	path	string	true	"User ID"
//	@Success	204
//	@Failure	403	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/groups/{groupID}/members/{userID} [delete]
func (h groupHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	err := h.service.RemoveMember(r.Context(), h.actor(r), chi.URLParam(r, "groupID"), chi.URLParam(r, "userID"))
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "group not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
