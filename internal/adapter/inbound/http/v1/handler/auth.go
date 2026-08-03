package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterAuthRoutes mounts invitation-only authentication endpoints under the v1 path.
func RegisterAuthRoutes(router chi.Router, authService *service.AuthService, cookieSecure bool) {
	handler := authHandler{service: authService, cookieSecure: cookieSecure}
	middleware := sharedauth.NewMiddleware(authenticator{service: authService}, authorizer{})
	router.Route(basePathV1+"/auth", func(routes chi.Router) {
		routes.Post("/login", handler.login)
		routes.Post("/refresh", handler.refresh)
		routes.Post("/logout", handler.logout)
		routes.Post("/invitations/accept", handler.acceptInvitation)
		routes.With(middleware.RequireAuthentication).Get("/me", handler.me)
		routes.With(middleware.RequireAuthentication, func(next http.Handler) http.Handler {
			return middleware.RequirePermission(string(domain.PermissionUsersInvite), next)
		}).Post("/invitations", handler.invite)
	})
}

type authHandler struct {
	service      *service.AuthService
	cookieSecure bool
}

func (h authHandler) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
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

func (h authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("billpiggy_refresh"); err == nil {
		_ = h.service.Logout(r.Context(), cookie.Value)
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h authHandler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token       string `json:"token"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := h.service.AcceptInvitation(r.Context(), request.Token, request.Password, request.DisplayName)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invitation is invalid or expired")
		return
	}
	writeJSON(w, http.StatusCreated, userResponse(user))
}

func (h authHandler) invite(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	actor := domain.AppUser{ID: identity.Subject, Email: identity.Email, Role: domain.UserRole(identity.Role)}
	var request struct {
		Email string          `json:"email"`
		Role  domain.UserRole `json:"role"`
	}
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

func (h authHandler) me(w http.ResponseWriter, r *http.Request) {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"id": identity.Subject, "email": identity.Email, "role": identity.Role})
}

func (h authHandler) writeSession(w http.ResponseWriter, session service.Session) {
	http.SetCookie(w, &http.Cookie{Name: "billpiggy_refresh", Value: session.RefreshToken, Path: basePathV1 + "/auth", Expires: session.RefreshTokenExpiry, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"access_token": session.AccessToken, "expires_at": session.AccessTokenExpiry})
}
func (h authHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "billpiggy_refresh", Value: "", Path: basePathV1 + "/auth", Expires: time.Unix(0, 0), MaxAge: -1, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode})
}

type authenticator struct{ service *service.AuthService }

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

func userResponse(user domain.AppUser) map[string]string {
	return map[string]string{"id": user.ID, "email": user.Email, "display_name": user.DisplayName, "role": string(user.Role)}
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
