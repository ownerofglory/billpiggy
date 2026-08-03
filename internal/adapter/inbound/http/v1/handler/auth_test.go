package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func TestAuthRoutesIssueAndRotateCookieBoundRefreshTokens(t *testing.T) {
	ctx := context.Background()
	repository := memory.NewIdentityRepository()
	authService, err := service.NewAuthService(repository, service.AuthConfig{JWTSecret: "01234567890123456789012345678901", BootstrapSuperAdminEmail: "admin@example.com", BootstrapSuperAdminPassword: "super-admin-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterAuthRoutes(router, authService, false)

	login := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"super-admin-password"}`))
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(loginResponse.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["access_token"] == "" {
		t.Fatal("access token missing")
	}
	cookie := loginResponse.Result().Cookies()[0]
	refresh := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/refresh", nil)
	refresh.AddCookie(cookie)
	refreshResponse := httptest.NewRecorder()
	router.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d", refreshResponse.Code)
	}
	if refreshResponse.Result().Cookies()[0].Value == cookie.Value {
		t.Fatal("refresh token was not rotated")
	}
}
