package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAuthRoutes mounts invitation-only authentication endpoints under the v1 path.
func RegisterAuthRoutes(router chi.Router, authService inbound.AuthService, cookieSecure bool) {
	handler := authHandler{service: authService, cookieSecure: cookieSecure}
	middleware := NewAuthMiddleware(authService)
	router.Route(basePathV1+"/auth", func(routes chi.Router) {
		routes.Post("/login", handler.login)
		routes.Post("/refresh", handler.refresh)
		routes.Post("/logout", handler.logout)
		routes.Post("/invitations/accept", handler.acceptInvitation)
		routes.Post("/password-reset", handler.requestPasswordReset)
		routes.Post("/password-reset/confirm", handler.confirmPasswordReset)
		routes.With(middleware.RequireAuthentication).Get("/me", handler.me)
		routes.With(middleware.RequireAuthentication, func(next http.Handler) http.Handler {
			return middleware.RequirePermission(string(domain.PermissionUsersInvite), next)
		}).Post("/invitations", handler.invite)
	})
}

// NewAuthMiddleware adapts the application auth service to reusable HTTP middleware.
func NewAuthMiddleware(authService inbound.AuthService) *sharedauth.Middleware {
	return sharedauth.NewMiddleware(authenticator{service: authService}, authorizer{})
}

type authHandler struct {
	service      inbound.AuthService
	cookieSecure bool
}

// login authenticates an invited user and sets a refresh-token cookie.
//
//	@Summary	Log in
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		loginRequest	true	"Credentials"
//	@Success	200		{object}	sessionResponse
//	@Failure	401		{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/login [post]
func (h authHandler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	session, err := h.service.Login(r.Context(), request.Email, request.Password)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	h.writeSession(w, session)
}

// refresh rotates the HttpOnly refresh token and returns a new access token.
//
//	@Summary	Refresh session
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	sessionResponse
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/refresh [post]
func (h authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("billpiggy_refresh")
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "refresh token required")
		return
	}
	session, err := h.service.Refresh(r.Context(), cookie.Value)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "refresh token is invalid or expired")
		return
	}
	h.writeSession(w, session)
}

// logout revokes the browser refresh token.
//
//	@Summary	Log out
//	@Tags		auth
//	@Success	204
//	@Router		/billpiggy/api/v1/auth/logout [post]
func (h authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("billpiggy_refresh"); err == nil {
		_ = h.service.Logout(r.Context(), cookie.Value)
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// acceptInvitation creates a user from an administrator-issued invitation.
//
//	@Summary	Accept invitation
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		acceptInvitationRequest	true	"Invitation details"
//	@Success	201		{object}	userResponseBody
//	@Failure	401		{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/invitations/accept [post]
func (h authHandler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var request acceptInvitationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := h.service.AcceptInvitation(r.Context(), request.Token, request.Password, request.DisplayName)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invitation is invalid or expired")
		return
	}
	writeJSON(w, http.StatusCreated, userPublic(user))
}

// invite creates a new invitation. It requires users:invite permission.
//
//	@Summary	Invite user
//	@Tags		auth, administration
//	@Accept		json
//	@Param		request	body	invitationRequest	true	"Invitation"
//	@Success	202
//	@Failure	401	{object}	map[string]string
//	@Failure	403	{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/invitations [post]
func (h authHandler) invite(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Email: identity.Email, Role: domain.UserRole(identity.Role)}
	var request invitationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	_, err := h.service.Invite(r.Context(), actor, request.Email, request.Role)
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invitation could not be created")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// requestPasswordReset queues a one-time reset email when the address
// belongs to an account.
//
// It always responds 202, whether or not the account exists and whether or
// not queuing succeeded: any difference in the response — status code,
// body, or timing — would let a caller enumerate registered emails. A real
// failure is only logged server-side.
//
//	@Summary	Request password reset
//	@Tags		auth
//	@Accept		json
//	@Param		request	body	passwordResetRequestBody	true	"Email"
//	@Success	202
//	@Router		/billpiggy/api/v1/auth/password-reset [post]
func (h authHandler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var request passwordResetRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := h.service.RequestPasswordReset(r.Context(), request.Email); err != nil {
		slog.Error("request password reset", "error", err)
	}
	w.WriteHeader(http.StatusAccepted)
}

// confirmPasswordReset sets a new password from the token emailed by
// requestPasswordReset.
//
//	@Summary	Confirm password reset
//	@Tags		auth
//	@Accept		json
//	@Param		request	body	passwordResetConfirmRequest	true	"Reset token and new password"
//	@Success	204
//	@Failure	400	{object}	map[string]string
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/password-reset/confirm [post]
func (h authHandler) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var request passwordResetConfirmRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	err := h.service.ResetPassword(r.Context(), request.Token, request.NewPassword)
	if errors.Is(err, service.ErrUnauthorized) {
		writeJSONError(w, http.StatusUnauthorized, "reset token is invalid or expired")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "password could not be reset")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me returns the authenticated user's identity.
//
//	@Summary	Get current user
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	currentUserResponse
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/auth/me [get]
func (h authHandler) me(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	writeJSON(w, http.StatusOK, currentUserResponse{ID: identity.Subject, Email: identity.Email, Role: identity.Role})
}

func (h authHandler) writeSession(w http.ResponseWriter, session service.Session) {
	http.SetCookie(w, &http.Cookie{Name: "billpiggy_refresh", Value: session.RefreshToken, Path: basePathV1 + "/auth", Expires: session.RefreshTokenExpiry, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, sessionResponse{AccessToken: session.AccessToken, ExpiresAt: session.AccessTokenExpiry})
}
func (h authHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "billpiggy_refresh", Value: "", Path: basePathV1 + "/auth", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode})
}

type authenticator struct{ service inbound.AuthService }

func (a authenticator) Authenticate(ctx context.Context, token string) (sharedauth.Identity, error) {
	user, err := a.service.AuthenticateAccessToken(ctx, token)
	if err != nil {
		return sharedauth.Identity{}, err
	}
	return sharedauth.Identity{Subject: user.ID, Email: user.Email, Role: string(user.Role)}, nil
}

type authorizer struct{}

func (authorizer) Allows(identity sharedauth.Identity, permission string) bool {
	return domain.UserRole(identity.Role).Allows(domain.Permission(permission))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type acceptInvitationRequest struct {
	Token       string `json:"token"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}
type invitationRequest struct {
	Email string          `json:"email"`
	Role  domain.UserRole `json:"role"`
}
type passwordResetRequestBody struct {
	Email string `json:"email"`
}
type passwordResetConfirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}
type userResponseBody struct {
	ID                        string                           `json:"id"`
	Email                     string                           `json:"email"`
	DisplayName               string                           `json:"display_name"`
	Role                      string                           `json:"role"`
	AccessBlocked             bool                             `json:"access_blocked"`
	EmailNotificationsEnabled bool                             `json:"email_notifications_enabled"`
	NotificationPreferences   map[domain.NotificationKind]bool `json:"notification_preferences"`
	AIEnabled                 bool                             `json:"ai_enabled"`
}
type currentUserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}
type sessionResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}
