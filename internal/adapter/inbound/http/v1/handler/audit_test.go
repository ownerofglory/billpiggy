package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

func newAuditHarness(t *testing.T) (chi.Router, string, string) {
	t.Helper()
	identity := memory.NewIdentityRepository()
	authService, err := service.NewAuthService(identity, service.AuthConfig{
		JWTSecret: "01234567890123456789012345678901", BootstrapSuperAdminEmail: "admin@example.com", BootstrapSuperAdminPassword: "super-admin-password",
	})
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	if err := authService.EnsureBootstrapSuperAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	adminSession, err := authService.Login(context.Background(), "admin@example.com", "super-admin-password")
	if err != nil {
		t.Fatalf("login as admin: %v", err)
	}
	admin, _ := authService.AuthenticateAccessToken(context.Background(), adminSession.AccessToken)

	invitation, err := authService.Invite(context.Background(), admin, "member@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite member: %v", err)
	}
	if _, err := authService.AcceptInvitation(context.Background(), invitation.RawToken, "member-password", "Member"); err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	memberSession, err := authService.Login(context.Background(), "member@example.com", "member-password")
	if err != nil {
		t.Fatalf("login as member: %v", err)
	}

	audit := memory.NewAuditRepository()
	if err := audit.AppendEntry(context.Background(), domain.AuditEntry{EventID: "event-1", ActorID: admin.ID, Action: "user_invited", ResourceType: "user", OccurredAt: time.Now()}); err != nil {
		t.Fatalf("append audit entry: %v", err)
	}
	auditService, err := service.NewAuditService(audit)
	if err != nil {
		t.Fatalf("build audit service: %v", err)
	}
	aiRequests := memory.NewAIRequestRepository()
	notifications := memory.NewNotificationRepository()
	usageService, err := service.NewAdminUsageService(identity, aiRequests, notifications, audit)
	if err != nil {
		t.Fatalf("build admin usage service: %v", err)
	}

	router := chi.NewRouter()
	handler.RegisterAuditRoutes(router, auditService, handler.NewAuthMiddleware(authService))
	handler.RegisterAdminUsageRoutes(router, usageService, handler.NewAuthMiddleware(authService))
	return router, adminSession.AccessToken, memberSession.AccessToken
}

func TestListAuditEntriesAllowsSuperAdmin(t *testing.T) {
	router, adminToken, _ := newAuditHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/audit", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var entries []domain.AuditEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0].EventID != "event-1" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestListAuditEntriesForbidsNonSuperAdmin(t *testing.T) {
	router, _, memberToken := newAuditHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/audit", nil)
	request.Header.Set("Authorization", "Bearer "+memberToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestListAuditEntriesRejectsInvalidTimestamp(t *testing.T) {
	router, adminToken, _ := newAuditHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/audit?from=not-a-time", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestAdminUsageSummaryAllowsSuperAdminOnly(t *testing.T) {
	router, adminToken, memberToken := newAuditHarness(t)

	adminRequest := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/admin/usage", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+adminToken)
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body = %s", adminResponse.Code, adminResponse.Body.String())
	}
	var summary service.UsageSummary
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.TotalUsers != 2 {
		t.Fatalf("total users = %d, want 2 (admin + member)", summary.TotalUsers)
	}

	memberRequest := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/admin/usage", nil)
	memberRequest.Header.Set("Authorization", "Bearer "+memberToken)
	memberResponse := httptest.NewRecorder()
	router.ServeHTTP(memberResponse, memberRequest)
	if memberResponse.Code != http.StatusForbidden {
		t.Fatalf("member status = %d, want 403", memberResponse.Code)
	}
}
