package handler_test

import (
	"bytes"
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
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// e2eApp wires every service against in-memory adapters exactly as
// cmd/billpiggy/main.go wires them against PostgreSQL, then registers every
// HTTP route on one router. Each test below drives one functional
// requirement's full flow through that router rather than a single handler
// in isolation.
type e2eApp struct {
	router        chi.Router
	identity      *memory.IdentityRepository
	notifications *memory.NotificationRepository
	reportService *service.ReportService
	// scheduledPaymentService is held so a flow test can drive the recurring
	// payment scheduler directly; nothing serves it over HTTP.
	scheduledPaymentService *service.ScheduledPaymentService
	engines                 []*outbox.Engine
}

func newE2EApp(t *testing.T, assistantAnswer string) *e2eApp {
	t.Helper()
	ctx := context.Background()

	identity := memory.NewIdentityRepository()
	expenses := memory.NewExpenseRepository()
	budgets := memory.NewBudgetRepository()
	groups := memory.NewGroupRepository()
	taxonomy := memory.NewTaxonomyRepository()
	analytics := memory.NewAnalyticsRepository()
	budgetUsage := memory.NewBudgetUsageRepository(budgets)
	audit := memory.NewAuditRepository()
	notifications := memory.NewNotificationRepository()
	objectRefs := memory.NewObjectReferenceRepository()
	aiRequests := memory.NewAIRequestRepository()
	reports := memory.NewReportRepository()
	scheduledPayments := memory.NewScheduledPaymentRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenses, budgets, analytics, budgetUsage, audit, notifications, objectRefs, taxonomy, reports, scheduledPayments, events)
	events.WithUnitOfWork(unit)

	authService, err := service.NewAuthService(identity, service.AuthConfig{
		JWTSecret: "01234567890123456789012345678901", BootstrapSuperAdminEmail: "admin@example.com", BootstrapSuperAdminPassword: "super-admin-password",
	})
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	authService = authService.WithObjectReferences(objectRefs).WithNotifications(notifications)
	if err := authService.EnsureBootstrapSuperAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	expenseService, err := service.NewExpenseService(expenses, events, unit)
	if err != nil {
		t.Fatalf("build expense service: %v", err)
	}
	expenseService = expenseService.WithObjectReferences(objectRefs).WithGroups(groups).WithTaxonomy(taxonomy)
	budgetService, err := service.NewBudgetService(budgets, events, groups, unit)
	if err != nil {
		t.Fatalf("build budget service: %v", err)
	}
	analyticsService, err := service.NewAnalyticsService(analytics, budgets)
	if err != nil {
		t.Fatalf("build analytics service: %v", err)
	}
	taxonomyService, err := service.NewTaxonomyService(taxonomy)
	if err != nil {
		t.Fatalf("build taxonomy service: %v", err)
	}
	groupService, err := service.NewGroupService(groups)
	if err != nil {
		t.Fatalf("build group service: %v", err)
	}
	objectStore := memory.NewObjectStore()
	reportService, err := service.NewReportService(reports, expenses, taxonomy, identity, objectStore, notifications)
	if err != nil {
		t.Fatalf("build report service: %v", err)
	}
	auditService, err := service.NewAuditService(audit)
	if err != nil {
		t.Fatalf("build audit service: %v", err)
	}
	adminUsageService, err := service.NewAdminUsageService(identity, aiRequests, notifications, audit)
	if err != nil {
		t.Fatalf("build admin usage service: %v", err)
	}
	assistantService, err := service.NewAssistantService(memory.NewAIProvider(assistantAnswer), expenses, budgets)
	if err != nil {
		t.Fatalf("build assistant service: %v", err)
	}
	scheduledPaymentService, err := service.NewScheduledPaymentService(scheduledPayments, events, groups, unit)
	if err != nil {
		t.Fatalf("build scheduled payment service: %v", err)
	}
	scheduledPaymentService = scheduledPaymentService.
		WithExpensePosting(expenses).
		WithNotifications(notifications).
		WithTaxonomy(taxonomy)

	app := &e2eApp{identity: identity, notifications: notifications, reportService: reportService, scheduledPaymentService: scheduledPaymentService}

	analyticsProjection, err := service.NewAnalyticsProjection(analytics)
	if err != nil {
		t.Fatalf("build analytics projection: %v", err)
	}
	budgetUsageProjection, err := service.NewBudgetUsageProjection(budgetUsage, notifications)
	if err != nil {
		t.Fatalf("build budget usage projection: %v", err)
	}
	auditProjection, err := service.NewAuditProjection(audit)
	if err != nil {
		t.Fatalf("build audit projection: %v", err)
	}
	for _, projection := range []outbox.Handler{analyticsProjection, budgetUsageProjection, auditProjection} {
		if err := events.EnsureSubscription(ctx, projection.Name()); err != nil {
			t.Fatalf("register subscription %s: %v", projection.Name(), err)
		}
		engine, err := outbox.NewEngine(events, projection, outbox.Options{Policy: outbox.DefaultPolicy()})
		if err != nil {
			t.Fatalf("build engine %s: %v", projection.Name(), err)
		}
		app.engines = append(app.engines, engine)
	}

	router := chi.NewRouter()
	middleware := handler.NewAuthMiddleware(authService)
	handler.RegisterAuthRoutes(router, authService, false)
	handler.RegisterUserRoutes(router, authService, middleware)
	handler.RegisterUploadRoutes(router, authService, expenseService, objectStore, middleware)
	handler.RegisterExpenseRoutes(router, expenseService, middleware)
	handler.RegisterBudgetRoutes(router, budgetService, middleware)
	handler.RegisterScheduledPaymentRoutes(router, scheduledPaymentService, middleware)
	handler.RegisterAnalyticsRoutes(router, analyticsService, middleware)
	handler.RegisterTaxonomyRoutes(router, taxonomyService, middleware)
	handler.RegisterGroupRoutes(router, groupService, middleware)
	handler.RegisterAssistantRoutes(router, assistantService, authService, middleware)
	handler.RegisterReportRoutes(router, reportService, objectStore, middleware)
	handler.RegisterAuditRoutes(router, auditService, middleware)
	handler.RegisterAdminUsageRoutes(router, adminUsageService, middleware)
	app.router = router
	return app
}

// drain steps every projection engine until each reports no more pending
// work, so an expense/budget event is fully reflected in its read models
// before a test asserts on them — deterministic, unlike a background Run
// loop a test would have to sleep to outrun.
func (a *e2eApp) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, engine := range a.engines {
		for {
			result, err := engine.Step(ctx)
			if err != nil {
				t.Fatalf("drain %s: %v", engine.Name(), err)
			}
			if result.Status == outbox.Idle {
				break
			}
		}
	}
}

// do sends a JSON request through the full router and decodes a JSON
// response, when out is non-nil.
func (a *e2eApp) do(t *testing.T, method, path, token string, body, out any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	a.router.ServeHTTP(response, request)
	if out != nil && response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatalf("decode response body %q: %v", response.Body.String(), err)
		}
	}
	return response
}

// loginAsAdmin logs in the bootstrap super-admin and returns their access
// token and user ID.
func (a *e2eApp) loginAsAdmin(t *testing.T) (token, userID string) {
	t.Helper()
	var session struct {
		AccessToken string `json:"access_token"`
	}
	response := a.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "super-admin-password"}, &session)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	var me struct {
		ID string `json:"id"`
	}
	a.do(t, http.MethodGet, "/billpiggy/api/v1/auth/me", session.AccessToken, nil, &me)
	return session.AccessToken, me.ID
}

// TestE2EInviteAcceptLoginRefreshLogout covers the full account-onboarding
// flow: an administrator invites a user, the user accepts, logs in, rotates
// their refresh token, and logs out.
func TestE2EInviteAcceptLoginRefreshLogout(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)

	inviteResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/invitations", adminToken, map[string]any{"email": "member@example.com", "role": "member"}, nil)
	if inviteResponse.Code != http.StatusAccepted {
		t.Fatalf("invite status = %d, body = %s", inviteResponse.Code, inviteResponse.Body.String())
	}
	deliveries := app.notifications.Deliveries()
	if len(deliveries) != 1 || deliveries[0].Kind != domain.NotificationInvitation {
		t.Fatalf("expected one invitation email queued, got %#v", deliveries)
	}
	rawToken := deliveries[0].Payload["token"]
	if rawToken == "" {
		t.Fatal("invitation payload carried no raw token")
	}

	acceptResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/invitations/accept", "", map[string]string{
		"token": rawToken, "password": "a-member-password", "display_name": "Member",
	}, nil)
	if acceptResponse.Code != http.StatusCreated {
		t.Fatalf("accept status = %d, body = %s", acceptResponse.Code, acceptResponse.Body.String())
	}

	var session struct {
		AccessToken string `json:"access_token"`
	}
	var cookie *http.Cookie
	loginResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": "member@example.com", "password": "a-member-password"}, &session)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	for _, c := range loginResponse.Result().Cookies() {
		if c.Name == "billpiggy_refresh" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login did not set a refresh cookie")
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/refresh", nil)
	refreshRequest.AddCookie(cookie)
	refreshResponse := httptest.NewRecorder()
	app.router.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", refreshResponse.Code, refreshResponse.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(cookie)
	logoutResponse := httptest.NewRecorder()
	app.router.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}
}

// TestE2EExpenseCRUDAndSearch covers creating, searching, filtering,
// updating, and deleting expenses through the full router.
func TestE2EExpenseCRUDAndSearch(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)

	create := func(title, categoryID string, amount int64) domain.ExpenseRecord {
		var created domain.ExpenseRecord
		response := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
			"title": title, "amount_minor": amount, "currency": "eur", "occurred_at": time.Now().Format(time.RFC3339),
			"category_id": categoryID, "status": "confirmed",
		}, &created)
		if response.Code != http.StatusCreated {
			t.Fatalf("create expense status = %d, body = %s", response.Code, response.Body.String())
		}
		return created
	}

	var categories []domain.ExpenseCategory
	app.do(t, http.MethodGet, "/billpiggy/api/v1/taxonomy/categories", adminToken, nil, &categories)
	if len(categories) == 0 {
		t.Fatal("expected default categories to be seeded")
	}
	categoryID := categories[0].ID

	cinema := create("Cinema night", categoryID, 2500)
	create("Groceries", categoryID, 4500)

	var listed []domain.ExpenseRecord
	app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/?q=cinema", adminToken, nil, &listed)
	if len(listed) != 1 || listed[0].ID != cinema.ID {
		t.Fatalf("search results = %#v, want only Cinema night", listed)
	}

	var fetched domain.ExpenseRecord
	getResponse := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+cinema.ID, adminToken, nil, &fetched)
	if getResponse.Code != http.StatusOK || fetched.Title != "Cinema night" {
		t.Fatalf("get expense = %#v, status %d", fetched, getResponse.Code)
	}

	var updated domain.ExpenseRecord
	updateResponse := app.do(t, http.MethodPut, "/billpiggy/api/v1/expenses/"+cinema.ID, adminToken, map[string]any{
		"title": "Cinema and popcorn", "amount_minor": 3600, "currency": "EUR", "occurred_at": cinema.OccurredAt.Format(time.RFC3339),
		"category_id": categoryID, "status": "confirmed",
	}, &updated)
	if updateResponse.Code != http.StatusOK || updated.AmountMinor != 3600 {
		t.Fatalf("update expense = %#v, status %d", updated, updateResponse.Code)
	}

	deleteResponse := app.do(t, http.MethodDelete, "/billpiggy/api/v1/expenses/"+cinema.ID, adminToken, nil, nil)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete expense status = %d", deleteResponse.Code)
	}
	afterDelete := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+cinema.ID, adminToken, nil, nil)
	if afterDelete.Code != http.StatusNotFound {
		t.Fatalf("get deleted expense status = %d, want 404", afterDelete.Code)
	}
}

// TestE2ESharedExpenseVisibility covers a group member reading an expense
// shared with their group, and an outsider being unable to.
func TestE2ESharedExpenseVisibility(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, adminID := app.loginAsAdmin(t)

	var group domain.UserGroup
	groupResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/groups/", adminToken, map[string]any{"name": "Roommates", "member_ids": []string{}}, &group)
	if groupResponse.Code != http.StatusCreated {
		t.Fatalf("create group status = %d, body = %s", groupResponse.Code, groupResponse.Body.String())
	}

	memberToken := app.inviteAndLogin(t, adminToken, "member@example.com", "member", "a-member-password")
	memberID := app.userIDFor(t, memberToken)
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/groups/"+group.ID+"/members/"+memberID, adminToken, nil, nil); response.Code != http.StatusNoContent {
		t.Fatalf("add member status = %d, body = %s", response.Code, response.Body.String())
	}

	var shared domain.ExpenseRecord
	createResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
		"title": "Shared dinner", "amount_minor": 4500, "currency": "EUR", "occurred_at": time.Now().Format(time.RFC3339),
		"shared_group_id": group.ID, "status": "confirmed",
	}, &shared)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create shared expense status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	var seenByMember domain.ExpenseRecord
	memberGet := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+shared.ID, memberToken, nil, &seenByMember)
	if memberGet.Code != http.StatusOK || seenByMember.ID != shared.ID {
		t.Fatalf("member get shared expense = %d, %#v", memberGet.Code, seenByMember)
	}

	outsiderToken := app.inviteAndLogin(t, adminToken, "outsider@example.com", "member", "an-outsider-password")
	outsiderGet := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+shared.ID, outsiderToken, nil, nil)
	if outsiderGet.Code != http.StatusNotFound {
		t.Fatalf("outsider get shared expense status = %d, want 404", outsiderGet.Code)
	}
	_ = adminID
}

// TestE2EBudgetThresholdQueuesAlert covers creating a budget, recording
// expenses against its category until the threshold is crossed, and
// confirming the projection queues a budget_alert notification.
func TestE2EBudgetThresholdQueuesAlert(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)

	var categories []domain.ExpenseCategory
	app.do(t, http.MethodGet, "/billpiggy/api/v1/taxonomy/categories", adminToken, nil, &categories)
	categoryID := categories[0].ID

	var budget domain.BudgetRecord
	budgetResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/budgets/", adminToken, map[string]any{
		"name": "Groceries budget", "category_id": categoryID, "amount_limit_minor": 10000, "currency": "EUR",
		"threshold_percent": 80, "period": "monthly",
	}, &budget)
	if budgetResponse.Code != http.StatusCreated {
		t.Fatalf("create budget status = %d, body = %s", budgetResponse.Code, budgetResponse.Body.String())
	}

	expenseResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
		"title": "Big grocery run", "amount_minor": 9000, "currency": "EUR", "occurred_at": time.Now().Format(time.RFC3339),
		"category_id": categoryID, "status": "confirmed",
	}, nil)
	if expenseResponse.Code != http.StatusCreated {
		t.Fatalf("create expense status = %d, body = %s", expenseResponse.Code, expenseResponse.Body.String())
	}

	app.drain(t)

	deliveries := app.notifications.Deliveries()
	found := false
	for _, delivery := range deliveries {
		if delivery.Kind == domain.NotificationBudgetAlert {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a budget_alert notification once the threshold crossed, got %#v", deliveries)
	}
}

// TestE2EAnalyticsRollup covers an expense flowing through the analytics
// projection and appearing in the rollup query endpoint.
func TestE2EAnalyticsRollup(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)

	var categories []domain.ExpenseCategory
	app.do(t, http.MethodGet, "/billpiggy/api/v1/taxonomy/categories", adminToken, nil, &categories)
	categoryID := categories[0].ID
	occurredAt := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	createResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
		"title": "Analytics expense", "amount_minor": 1500, "currency": "EUR", "occurred_at": occurredAt.Format(time.RFC3339),
		"category_id": categoryID, "status": "confirmed",
	}, nil)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create expense status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	app.drain(t)

	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	var rollups []domain.ExpenseRollup
	url := "/billpiggy/api/v1/analytics/expenses?period=month&from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	rollupResponse := app.do(t, http.MethodGet, url, adminToken, nil, &rollups)
	if rollupResponse.Code != http.StatusOK {
		t.Fatalf("rollup status = %d, body = %s", rollupResponse.Code, rollupResponse.Body.String())
	}
	var total int64
	for _, rollup := range rollups {
		total += rollup.AmountMinor
	}
	if total != 1500 {
		t.Fatalf("rollup total = %d, want 1500: %#v", total, rollups)
	}
}

// TestE2EReportGenerateAndDownload covers the scheduler generating a due
// report and a user downloading it.
func TestE2EReportGenerateAndDownload(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, adminID := app.loginAsAdmin(t)

	if _, err := app.reportService.GenerateDue(context.Background(), time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("generate due reports: %v", err)
	}

	var reports []domain.Report
	listResponse := app.do(t, http.MethodGet, "/billpiggy/api/v1/reports/", adminToken, nil, &reports)
	if listResponse.Code != http.StatusOK || len(reports) == 0 {
		t.Fatalf("list reports status = %d, reports = %#v", listResponse.Code, reports)
	}
	for _, report := range reports {
		if report.OwnerID != adminID {
			t.Fatalf("report owner = %q, want %q", report.OwnerID, adminID)
		}
	}
	downloadResponse := app.do(t, http.MethodGet, "/billpiggy/api/v1/reports/"+reports[0].ID+"/download", adminToken, nil, nil)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download report status = %d", downloadResponse.Code)
	}
	if downloadResponse.Body.Len() == 0 {
		t.Fatal("downloaded report body is empty")
	}
}

// TestE2EAdminUserManagement covers an administrator listing, changing the
// role/access of, and deleting an ordinary user.
func TestE2EAdminUserManagement(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	memberToken := app.inviteAndLogin(t, adminToken, "member@example.com", "member", "a-member-password")
	memberID := app.userIDFor(t, memberToken)

	// The API's user response is snake_case JSON (userResponseBody, internal
	// to the handler package); domain.AppUser carries no json tags at all, so
	// decoding straight into it would silently mismatch every underscored
	// field (access_blocked) while accidentally matching simple ones (role) —
	// this local type mirrors the wire shape instead.
	type apiUser struct {
		ID            string `json:"id"`
		Role          string `json:"role"`
		AccessBlocked bool   `json:"access_blocked"`
	}

	var users []apiUser
	listResponse := app.do(t, http.MethodGet, "/billpiggy/api/v1/users/", adminToken, nil, &users)
	if listResponse.Code != http.StatusOK || len(users) != 2 {
		t.Fatalf("list users status = %d, users = %#v", listResponse.Code, users)
	}

	var managed apiUser
	manageResponse := app.do(t, http.MethodPut, "/billpiggy/api/v1/users/"+memberID, adminToken, map[string]any{"role": "admin", "access_blocked": true}, &managed)
	if manageResponse.Code != http.StatusOK || managed.Role != string(domain.RoleAdmin) || !managed.AccessBlocked {
		t.Fatalf("manage user = %#v, status %d", managed, manageResponse.Code)
	}

	// The now-blocked user cannot authenticate.
	blockedMeResponse := app.do(t, http.MethodGet, "/billpiggy/api/v1/auth/me", memberToken, nil, nil)
	if blockedMeResponse.Code != http.StatusUnauthorized {
		t.Fatalf("blocked user /me status = %d, want 401", blockedMeResponse.Code)
	}

	deleteResponse := app.do(t, http.MethodDelete, "/billpiggy/api/v1/users/"+memberID, adminToken, nil, nil)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete user status = %d", deleteResponse.Code)
	}
}

// TestE2EAssistantChat covers a full request through the SSE assistant
// endpoint using a scripted provider.
func TestE2EAssistantChat(t *testing.T) {
	app := newE2EApp(t, "You spent 25 euro on cinema this month.")
	adminToken, _ := app.loginAsAdmin(t)

	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", bytes.NewReader([]byte(`{"message":"how much did I spend?"}`)))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("assistant chat status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !bytes.Contains([]byte(body), []byte("message.started")) || !bytes.Contains([]byte(body), []byte("message.delta")) {
		t.Fatalf("assistant chat response missing expected SSE frames: %s", body)
	}
}

// inviteAndLogin invites email as role, accepts the invitation, and logs in,
// returning the new user's access token.
func (a *e2eApp) inviteAndLogin(t *testing.T, adminToken, email, role, password string) string {
	t.Helper()
	inviteResponse := a.do(t, http.MethodPost, "/billpiggy/api/v1/auth/invitations", adminToken, map[string]any{"email": email, "role": role}, nil)
	if inviteResponse.Code != http.StatusAccepted {
		t.Fatalf("invite %s status = %d, body = %s", email, inviteResponse.Code, inviteResponse.Body.String())
	}
	var rawToken string
	for _, delivery := range a.notifications.Deliveries() {
		if delivery.RecipientEmail == email && delivery.Kind == domain.NotificationInvitation {
			rawToken = delivery.Payload["token"]
		}
	}
	if rawToken == "" {
		t.Fatalf("no invitation token found for %s", email)
	}
	if response := a.do(t, http.MethodPost, "/billpiggy/api/v1/auth/invitations/accept", "", map[string]string{
		"token": rawToken, "password": password, "display_name": email,
	}, nil); response.Code != http.StatusCreated {
		t.Fatalf("accept invitation for %s status = %d, body = %s", email, response.Code, response.Body.String())
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if response := a.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &session); response.Code != http.StatusOK {
		t.Fatalf("login as %s status = %d", email, response.Code)
	}
	return session.AccessToken
}

// userIDFor returns the subject of an access token via /auth/me.
func (a *e2eApp) userIDFor(t *testing.T, token string) string {
	t.Helper()
	var me struct {
		ID string `json:"id"`
	}
	if response := a.do(t, http.MethodGet, "/billpiggy/api/v1/auth/me", token, nil, &me); response.Code != http.StatusOK {
		t.Fatalf("/auth/me status = %d", response.Code)
	}
	return me.ID
}
