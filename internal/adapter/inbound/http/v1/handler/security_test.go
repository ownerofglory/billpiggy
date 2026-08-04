package handler_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// TestAuthzMatrix confirms permission gates hold for every role on
// endpoints an ordinary administrator (not just the bootstrap super-admin)
// should reach: no token is unauthenticated, a member without the required
// permission is forbidden, and an admin with it succeeds.
func TestAuthzMatrix(t *testing.T) {
	app := newE2EApp(t, "")
	superAdminToken, _ := app.loginAsAdmin(t)
	adminToken := app.inviteAndLogin(t, superAdminToken, "admin@example.test", "admin", "a-plain-admin-password")
	memberToken := app.inviteAndLogin(t, superAdminToken, "member@example.com", "member", "a-member-password")

	cases := []struct {
		name         string
		method, path string
		body         any
		memberStatus int
		adminStatus  int
	}{
		{name: "member can list budgets", method: http.MethodGet, path: "/billpiggy/api/v1/budgets/", memberStatus: http.StatusOK, adminStatus: http.StatusOK},
		{name: "member cannot create a group", method: http.MethodPost, path: "/billpiggy/api/v1/groups/", body: map[string]any{"name": "x"}, memberStatus: http.StatusForbidden, adminStatus: http.StatusCreated},
		{name: "member cannot manage users", method: http.MethodGet, path: "/billpiggy/api/v1/users/", memberStatus: http.StatusForbidden, adminStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if response := app.do(t, tc.method, tc.path, "", tc.body, nil); response.Code != http.StatusUnauthorized {
				t.Fatalf("no token: status = %d, want 401", response.Code)
			}
			if response := app.do(t, tc.method, tc.path, memberToken, tc.body, nil); response.Code != tc.memberStatus {
				t.Fatalf("member: status = %d, want %d, body = %s", response.Code, tc.memberStatus, response.Body.String())
			}
			if response := app.do(t, tc.method, tc.path, adminToken, tc.body, nil); response.Code != tc.adminStatus {
				t.Fatalf("admin: status = %d, want %d, body = %s", response.Code, tc.adminStatus, response.Body.String())
			}
		})
	}
}

// TestAuditAndUsageEndpointsAreSuperAdminOnly confirms GET /audit and
// GET /admin/usage are forbidden to an ordinary administrator — the one
// permission (audit:read) UserRole.Allows deliberately withholds from
// RoleAdmin — and succeed only for the bootstrap super-admin.
func TestAuditAndUsageEndpointsAreSuperAdminOnly(t *testing.T) {
	app := newE2EApp(t, "")
	superAdminToken, _ := app.loginAsAdmin(t)
	adminToken := app.inviteAndLogin(t, superAdminToken, "admin@example.test", "admin", "a-plain-admin-password")

	for _, path := range []string{"/billpiggy/api/v1/audit", "/billpiggy/api/v1/admin/usage"} {
		if response := app.do(t, http.MethodGet, path, adminToken, nil, nil); response.Code != http.StatusForbidden {
			t.Fatalf("plain admin %s status = %d, want 403, body = %s", path, response.Code, response.Body.String())
		}
		if response := app.do(t, http.MethodGet, path, superAdminToken, nil, nil); response.Code != http.StatusOK {
			t.Fatalf("super-admin %s status = %d, want 200, body = %s", path, response.Code, response.Body.String())
		}
	}
}

// TestCrossTenantIsolation confirms one user's budgets, categories, tags,
// and reports are invisible to and unmodifiable by another, unrelated user.
func TestCrossTenantIsolation(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	ownerToken := app.inviteAndLogin(t, adminToken, "owner@example.com", "member", "an-owner-password")
	outsiderToken := app.inviteAndLogin(t, adminToken, "outsider@example.com", "member", "an-outsider-password")
	outsiderID := app.userIDFor(t, outsiderToken)

	var categories []domain.ExpenseCategory
	app.do(t, http.MethodGet, "/billpiggy/api/v1/taxonomy/categories", ownerToken, nil, &categories)
	categoryID := categories[0].ID

	// Budget: created by owner, invisible to and unmodifiable by outsider.
	var budget domain.BudgetRecord
	createBudget := app.do(t, http.MethodPost, "/billpiggy/api/v1/budgets/", ownerToken, map[string]any{
		"name": "Groceries", "category_id": categoryID, "amount_limit_minor": 10000, "currency": "EUR", "threshold_percent": 80, "period": "monthly",
	}, &budget)
	if createBudget.Code != http.StatusCreated {
		t.Fatalf("create budget status = %d, body = %s", createBudget.Code, createBudget.Body.String())
	}
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/budgets/"+budget.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider get budget status = %d, want 404", response.Code)
	}
	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/budgets/"+budget.ID, outsiderToken, map[string]any{
		"name": "Hijacked", "category_id": categoryID, "amount_limit_minor": 1, "currency": "EUR", "threshold_percent": 1, "period": "monthly",
	}, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider update budget status = %d, want 404", response.Code)
	}
	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/budgets/"+budget.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider delete budget status = %d, want 404", response.Code)
	}

	// Category: created by owner, unmodifiable by outsider.
	var category domain.ExpenseCategory
	createCategory := app.do(t, http.MethodPost, "/billpiggy/api/v1/taxonomy/categories", ownerToken, map[string]string{"name": "Private", "color": "#000000"}, &category)
	if createCategory.Code != http.StatusCreated {
		t.Fatalf("create category status = %d, body = %s", createCategory.Code, createCategory.Body.String())
	}
	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/taxonomy/categories/"+category.ID, outsiderToken, map[string]string{"name": "Hijacked"}, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider update category status = %d, want 404", response.Code)
	}
	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/taxonomy/categories/"+category.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider delete category status = %d, want 404", response.Code)
	}

	// Tag: same shape as category.
	var tag domain.ExpenseTag
	createTag := app.do(t, http.MethodPost, "/billpiggy/api/v1/taxonomy/tags", ownerToken, map[string]string{"name": "Private tag"}, &tag)
	if createTag.Code != http.StatusCreated {
		t.Fatalf("create tag status = %d, body = %s", createTag.Code, createTag.Body.String())
	}
	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/taxonomy/tags/"+tag.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider delete tag status = %d, want 404", response.Code)
	}

	// Expense: owner's private (non-shared) expense is invisible to outsider.
	var expense domain.ExpenseRecord
	createExpense := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", ownerToken, map[string]any{
		"title": "Private expense", "amount_minor": 100, "currency": "EUR", "occurred_at": time.Now().Format(time.RFC3339), "status": "confirmed",
	}, &expense)
	if createExpense.Code != http.StatusCreated {
		t.Fatalf("create expense status = %d, body = %s", createExpense.Code, createExpense.Body.String())
	}
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+expense.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider get private expense status = %d, want 404", response.Code)
	}

	// Reports: outsider's report list never includes owner's reports.
	if _, err := app.reportService.GenerateDue(t.Context(), time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("generate due reports: %v", err)
	}
	var outsiderReports []domain.Report
	app.do(t, http.MethodGet, "/billpiggy/api/v1/reports/", outsiderToken, nil, &outsiderReports)
	for _, report := range outsiderReports {
		if report.OwnerID != outsiderID {
			t.Fatalf("outsider's report list included a report owned by %q", report.OwnerID)
		}
	}
	var ownerReports []domain.Report
	app.do(t, http.MethodGet, "/billpiggy/api/v1/reports/", ownerToken, nil, &ownerReports)
	for _, report := range ownerReports {
		for _, outsiderReport := range outsiderReports {
			if report.ID == outsiderReport.ID {
				t.Fatalf("outsider's report list leaked owner's report %s", report.ID)
			}
		}
	}
}

// TestGroupManagementIsRestrictedToCreatorOrSuperAdmin confirms an admin who
// did not create a group cannot rename, delete, or change its membership.
func TestGroupManagementIsRestrictedToCreatorOrSuperAdmin(t *testing.T) {
	app := newE2EApp(t, "")
	superAdminToken, _ := app.loginAsAdmin(t)
	creatorToken := app.inviteAndLogin(t, superAdminToken, "creator@example.com", "admin", "a-creator-password")
	otherAdminToken := app.inviteAndLogin(t, superAdminToken, "other-admin@example.com", "admin", "an-other-password")
	memberToken := app.inviteAndLogin(t, superAdminToken, "member@example.com", "member", "a-member-password")
	memberID := app.userIDFor(t, memberToken)

	var group domain.UserGroup
	createResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/groups/", creatorToken, map[string]any{"name": "Household", "member_ids": []string{}}, &group)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create group status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/groups/"+group.ID, otherAdminToken, map[string]any{"name": "Hijacked"}, nil); response.Code != http.StatusForbidden {
		t.Fatalf("other admin update group status = %d, want 403, body = %s", response.Code, response.Body.String())
	}
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/groups/"+group.ID+"/members/"+memberID, otherAdminToken, nil, nil); response.Code != http.StatusForbidden {
		t.Fatalf("other admin add member status = %d, want 403", response.Code)
	}
	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/groups/"+group.ID, otherAdminToken, nil, nil); response.Code != http.StatusForbidden {
		t.Fatalf("other admin delete group status = %d, want 403", response.Code)
	}

	// The creator can still manage their own group.
	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/groups/"+group.ID, creatorToken, map[string]any{"name": "Roommates"}, nil); response.Code != http.StatusOK {
		t.Fatalf("creator update group status = %d, body = %s", response.Code, response.Body.String())
	}
}

// TestRefreshTokenReplayIsRejected confirms that once a refresh token has
// rotated, replaying the pre-rotation cookie is rejected rather than
// silently issuing another session — a stolen old cookie must not still work.
func TestRefreshTokenReplayIsRejected(t *testing.T) {
	app := newE2EApp(t, "")
	var session struct {
		AccessToken string `json:"access_token"`
	}
	loginResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "super-admin-password"}, &session)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.Code)
	}
	originalCookie := loginResponse.Result().Cookies()[0]

	rotateRequest := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/refresh", nil)
	rotateRequest.AddCookie(originalCookie)
	rotateResponse := httptest.NewRecorder()
	app.router.ServeHTTP(rotateResponse, rotateRequest)
	if rotateResponse.Code != http.StatusOK {
		t.Fatalf("rotate status = %d", rotateResponse.Code)
	}
	rotatedCookie := rotateResponse.Result().Cookies()[0]
	if rotatedCookie.Value == originalCookie.Value {
		t.Fatal("refresh token was not rotated")
	}

	// Replaying the pre-rotation cookie must fail now that it has been
	// superseded, not silently succeed as if it were still current.
	replayRequest := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/refresh", nil)
	replayRequest.AddCookie(originalCookie)
	replayResponse := httptest.NewRecorder()
	app.router.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", replayResponse.Code)
	}

	// The rotated cookie still works.
	useRotatedRequest := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/auth/refresh", nil)
	useRotatedRequest.AddCookie(rotatedCookie)
	useRotatedResponse := httptest.NewRecorder()
	app.router.ServeHTTP(useRotatedResponse, useRotatedRequest)
	if useRotatedResponse.Code != http.StatusOK {
		t.Fatalf("using the rotated cookie status = %d, want 200", useRotatedResponse.Code)
	}
}

// TestReceiptUploadRejectsOversizedFiles confirms the 10 MiB upload limit is
// enforced, not just documented.
func TestReceiptUploadRejectsOversizedFiles(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	var expense domain.ExpenseRecord
	createResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
		"title": "Big receipt", "amount_minor": 100, "currency": "EUR", "occurred_at": time.Now().Format(time.RFC3339), "status": "confirmed",
	}, &expense)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create expense status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	oversized := bytes.Repeat([]byte("a"), 11<<20) // 11 MiB, over the 10 MiB limit
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "huge.jpg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(oversized); err != nil {
		t.Fatalf("write oversized content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/expenses/"+expense.ID+"/receipt", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	app.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized upload status = %d, want 400, body = %s", response.Code, response.Body.String())
	}
}

// TestReceiptUploadIgnoresTheDeclaredExtensionAndContentType confirms the
// upload path trusts sniffed content, not the filename or declared
// Content-Type header — a client claiming a PNG is really a JPEG (or naming
// a script "photo.jpg") does not get to choose how its bytes are treated.
func TestReceiptUploadIgnoresTheDeclaredExtensionAndContentType(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	var expense domain.ExpenseRecord
	createResponse := app.do(t, http.MethodPost, "/billpiggy/api/v1/expenses/", adminToken, map[string]any{
		"title": "Lying extension", "amount_minor": 100, "currency": "EUR", "occurred_at": time.Now().Format(time.RFC3339), "status": "confirmed",
	}, &expense)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create expense status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	// Plain text content, but named and declared as a JPEG.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="totally-a-photo.jpg"`},
		"Content-Type":        {"image/jpeg"},
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("#!/bin/sh\necho not a photo\n")); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/expenses/"+expense.ID+"/receipt", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	app.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("shell-script-as-jpeg upload status = %d, want 400 (content sniffing must reject it), body = %s", response.Code, response.Body.String())
	}
}

// TestDownloadReceiptOpaqueIDCannotEscapeItsScope confirms a path-traversal
// attempt in the expenseID URL segment cannot be used to reach another
// expense's or another owner's receipt: chi's {expenseID} pattern never
// matches a "/" in the segment, and the ID is used only as an opaque lookup
// key, never concatenated into a filesystem or object-store path a traversal
// sequence could redirect.
func TestDownloadReceiptOpaqueIDCannotEscapeItsScope(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	for _, attempt := range []string{"..", "..%2f..%2fetc%2fpasswd", "%2e%2e%2f%2e%2e%2fexpenses%2fsome-id"} {
		response := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/"+attempt+"/receipt", adminToken, nil, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("path-traversal attempt %q status = %d, want 404 (opaque lookup miss)", attempt, response.Code)
		}
	}
}
