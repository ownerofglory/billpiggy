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

func newReportHarness(t *testing.T) (chi.Router, string, string, *service.ReportService) {
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
	session, err := authService.Login(context.Background(), "admin@example.com", "super-admin-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	claims, _ := authService.AuthenticateAccessToken(context.Background(), session.AccessToken)

	expenses := memory.NewExpenseRepository()
	if err := expenses.CreateExpense(context.Background(), domain.ExpenseRecord{
		ID: "expense-1", OwnerID: claims.ID, Title: "Cinema", AmountMinor: 2500, Currency: "EUR",
		OccurredAt: time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC), CategoryName: "Entertainment", Status: domain.ExpenseConfirmed,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	objects := memory.NewObjectStore()
	reportService, err := service.NewReportService(memory.NewReportRepository(), expenses, memory.NewTaxonomyRepository(), identity, objects, memory.NewNotificationRepository())
	if err != nil {
		t.Fatalf("build report service: %v", err)
	}
	if _, err := reportService.GenerateDue(context.Background(), time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("generate due reports: %v", err)
	}

	router := chi.NewRouter()
	handler.RegisterReportRoutes(router, reportService, objects, handler.NewAuthMiddleware(authService))
	return router, session.AccessToken, claims.ID, reportService
}

func TestListReportsReturnsOwnerScopedReports(t *testing.T) {
	router, token, _, _ := newReportHarness(t)

	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/reports", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var reports []domain.Report
	if err := json.Unmarshal(response.Body.Bytes(), &reports); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(reports) != 6 {
		t.Fatalf("reports = %d, want 6 (week/month/year x csv/pdf)", len(reports))
	}
}

func TestListReportsRequiresAuthentication(t *testing.T) {
	router, _, _, _ := newReportHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/reports", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestDownloadReportRedirectsToStoredObject(t *testing.T) {
	router, token, ownerID, reportService := newReportHarness(t)
	listed, err := reportService.ListReports(context.Background(), ownerID)
	if err != nil || len(listed) == 0 {
		t.Fatalf("list reports: %#v, %v", listed, err)
	}

	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/reports/"+listed[0].ID+"/download", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	// The in-memory object store cannot presign, so the handler falls back to
	// streaming the object directly rather than redirecting.
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.Len() == 0 {
		t.Fatal("empty report body")
	}
}

func TestDownloadReportNotFoundForUnknownID(t *testing.T) {
	router, token, _, _ := newReportHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/reports/does-not-exist/download", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}
