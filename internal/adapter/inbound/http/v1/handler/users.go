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

// RegisterUserRoutes mounts profile and administrator user-management endpoints.
func RegisterUserRoutes(router chi.Router, auth inbound.AuthService, middleware *sharedauth.Middleware) {
	h := userHandler{service: auth}
	router.Route(basePathV1+"/users", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/me/profile", h.profile)
		routes.Put("/me/profile", h.updateProfile)
		routes.Put("/me/notification-preferences", h.updateNotificationPreferences)
		routes.Post("/me/password", h.changePassword)
		routes.With(permission(middleware, domain.PermissionUsersManage)).Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionUsersManage)).Put("/{userID}", h.manage)
		routes.With(permission(middleware, domain.PermissionUsersManage)).Delete("/{userID}", h.delete)
	})
}

type userHandler struct{ service inbound.AuthService }
type profileRequest struct {
	DisplayName               string `json:"display_name"`
	Email                     string `json:"email"`
	EmailNotificationsEnabled bool   `json:"email_notifications_enabled"`
	AIEnabled                 bool   `json:"ai_enabled"`
}
type manageUserRequest struct {
	Role          domain.UserRole `json:"role"`
	AccessBlocked bool            `json:"access_blocked"`
}

func (h userHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}
func userPublic(value domain.AppUser) userResponseBody {
	return userResponseBody{
		ID: value.ID, Email: value.Email, DisplayName: value.DisplayName, Role: string(value.Role), AccessBlocked: value.AccessBlocked,
		EmailNotificationsEnabled: value.EmailNotificationsEnabled, NotificationPreferences: value.NotificationPreferences, AIEnabled: value.AIEnabled,
	}
}

// profile returns the authenticated user's profile.
//
//	@Summary	Get profile
//	@Tags		users
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Success	200	{object}	userResponseBody
//	@Router		/billpiggy/api/v1/users/me/profile [get]
func (h userHandler) profile(w http.ResponseWriter, r *http.Request) {
	user, err := h.service.GetProfile(r.Context(), h.actor(r).ID)
	if err != nil {
		writeJSONError(w, 404, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, userPublic(user))
}

// updateProfile changes the authenticated user's profile and notification preference.
//
//	@Summary	Update profile
//	@Tags		users
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		profileRequest	true	"Profile"
//	@Success	200		{object}	userResponseBody
//	@Router		/billpiggy/api/v1/users/me/profile [put]
func (h userHandler) updateProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := h.service.UpdateProfile(r.Context(), h.actor(r).ID, req.DisplayName, req.Email, req.EmailNotificationsEnabled, req.AIEnabled)
	if err != nil {
		writeJSONError(w, 400, "profile could not be updated")
		return
	}
	writeJSON(w, 200, userPublic(user))
}

// updateNotificationPreferences overrides EmailNotificationsEnabled per
// notification kind. A kind omitted from the request body falls back to the
// master switch set through updateProfile.
//
//	@Summary	Update notification preferences
//	@Tags		users
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		map[string]bool	true	"Per-kind overrides, keyed by notification kind (invitation, budget_alert, report_ready, access_changed)"
//	@Success	200		{object}	userResponseBody
//	@Router		/billpiggy/api/v1/users/me/notification-preferences [put]
func (h userHandler) updateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	var preferences map[domain.NotificationKind]bool
	if !decodeJSON(w, r, &preferences) {
		return
	}
	user, err := h.service.UpdateNotificationPreferences(r.Context(), h.actor(r).ID, preferences)
	if err != nil {
		writeJSONError(w, 400, "notification preferences could not be updated")
		return
	}
	writeJSON(w, 200, userPublic(user))
}

// changePasswordRequest carries the current and new password.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword verifies the current password, sets a new one, and revokes
// every other live session so a leaked refresh token stops working.
//
//	@Summary	Change password
//	@Tags		users
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Param		request	body	changePasswordRequest	true	"Passwords"
//	@Success	204
//	@Failure	400	{object}	map[string]string
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/users/me/password [post]
func (h userHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	err := h.service.ChangePassword(r.Context(), h.actor(r).ID, req.CurrentPassword, req.NewPassword)
	if errors.Is(err, service.ErrUnauthorized) {
		writeJSONError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "password could not be changed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// list returns all users to an administrator.
//
//	@Summary	List users
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Success	200	{array}	userResponseBody
//	@Router		/billpiggy/api/v1/users/ [get]
func (h userHandler) list(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.ListUsers(r.Context(), h.actor(r))
	if err != nil {
		writeJSONError(w, 403, "permission denied")
		return
	}
	values := make([]userResponseBody, 0, len(users))
	for _, user := range users {
		values = append(values, userPublic(user))
	}
	writeJSON(w, 200, values)
}

// manage changes an ordinary user's role or access state.
//
//	@Summary	Manage user
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		userID	path		string				true	"User ID"
//	@Param		request	body		manageUserRequest	true	"User access"
//	@Success	200		{object}	userResponseBody
//	@Failure	403		{object}	map[string]string
//	@Router		/billpiggy/api/v1/users/{userID} [put]
func (h userHandler) manage(w http.ResponseWriter, r *http.Request) {
	var req manageUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := h.service.ManageUser(r.Context(), h.actor(r), chi.URLParam(r, "userID"), req.Role, req.AccessBlocked)
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, 403, "super admin cannot be changed")
		return
	}
	if err != nil {
		writeJSONError(w, 400, "user could not be updated")
		return
	}
	writeJSON(w, 200, userPublic(user))
}

// delete removes an ordinary user while preserving the super-admin account.
//
//	@Summary	Delete user
//	@Tags		administration
//	@Security	ApiKeyAuth
//	@Param		userID	path	string	true	"User ID"
//	@Success	204
//	@Failure	403	{object}	map[string]string
//	@Router		/billpiggy/api/v1/users/{userID} [delete]
func (h userHandler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.service.DeleteUser(r.Context(), h.actor(r), chi.URLParam(r, "userID"))
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, 403, "super admin cannot be deleted")
		return
	}
	if err != nil {
		writeJSONError(w, 404, "user not found")
		return
	}
	w.WriteHeader(204)
}
