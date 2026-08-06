package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// apiScheduledPayment mirrors the JSON domain.ScheduledPayment serialises to.
// Tests decode into this rather than the domain type so a silent rename of a
// json tag fails here instead of quietly deserialising to a zero value.
type apiScheduledPayment struct {
	ID                 string     `json:"id"`
	OwnerID            string     `json:"ownerID"`
	Title              string     `json:"title"`
	AmountMinor        int64      `json:"amountMinor"`
	Currency           string     `json:"currency"`
	Frequency          string     `json:"frequency"`
	CustomIntervalDays int        `json:"customIntervalDays"`
	StartDate          time.Time  `json:"startDate"`
	NextDueAt          time.Time  `json:"nextDueAt"`
	AutoPost           bool       `json:"autoPost"`
	ReminderDaysBefore int        `json:"reminderDaysBefore"`
	Paused             bool       `json:"paused"`
	EndDate            *time.Time `json:"endDate"`
}

func rentBody(start time.Time) map[string]any {
	return map[string]any{
		"title":         "Rent",
		"amount_minor":  120000,
		"currency":      "EUR",
		"frequency":     "monthly",
		"start_date":    start.Format(time.RFC3339),
		"auto_post":     true,
		"category_name": "Home",
	}
}

// TestE2EScheduledPaymentLifecycle drives the recurring-payment requirement
// end to end through the real router: create a monthly rent, see it listed,
// edit it, have the scheduler post the expense it owes, then delete it.
func TestE2EScheduledPaymentLifecycle(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	var created apiScheduledPayment
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", adminToken, rentBody(start), &created); response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	if created.ID == "" || created.Title != "Rent" || created.AmountMinor != 120000 {
		t.Fatalf("created payment = %#v", created)
	}
	if !created.NextDueAt.Equal(start) {
		t.Fatalf("nextDueAt = %s, want the start date %s", created.NextDueAt, start)
	}

	var listed []apiScheduledPayment
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/scheduled-payments/", adminToken, nil, &listed); response.Code != http.StatusOK {
		t.Fatalf("list status = %d", response.Code)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %#v, want the one created payment", listed)
	}

	var fetched apiScheduledPayment
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/scheduled-payments/"+created.ID, adminToken, nil, &fetched); response.Code != http.StatusOK {
		t.Fatalf("get status = %d", response.Code)
	}
	if fetched.ID != created.ID {
		t.Fatalf("fetched %s, want %s", fetched.ID, created.ID)
	}

	// A rent rise keeps the schedule but changes the amount.
	raise := rentBody(start)
	raise["amount_minor"] = 130000
	var updated apiScheduledPayment
	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/scheduled-payments/"+created.ID, adminToken, raise, &updated); response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}
	if updated.AmountMinor != 130000 || !updated.NextDueAt.Equal(start) {
		t.Fatalf("updated = %#v, want the new amount on the same schedule", updated)
	}

	// The scheduler posts the occurrence, which then shows up as an ordinary
	// expense on the expenses endpoint.
	if _, err := app.scheduledPaymentService.PostDue(context.Background(), start.Add(time.Hour)); err != nil {
		t.Fatalf("post due: %v", err)
	}
	app.drain(t)

	var expenses []struct {
		Title       string `json:"title"`
		AmountMinor int64  `json:"amountMinor"`
		Status      string `json:"status"`
	}
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/expenses/?q=Rent", adminToken, nil, &expenses); response.Code != http.StatusOK {
		t.Fatalf("list expenses status = %d", response.Code)
	}
	if len(expenses) != 1 || expenses[0].AmountMinor != 130000 || expenses[0].Status != "confirmed" {
		t.Fatalf("auto-posted expenses = %#v, want one confirmed 130000 rent", expenses)
	}

	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/scheduled-payments/"+created.ID, adminToken, nil, nil); response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.Code)
	}
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/scheduled-payments/"+created.ID, adminToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", response.Code)
	}
}

func TestE2EScheduledPaymentRequiresAuthentication(t *testing.T) {
	app := newE2EApp(t, "")
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/billpiggy/api/v1/scheduled-payments/", nil},
		{http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", rentBody(start)},
		{http.MethodGet, "/billpiggy/api/v1/scheduled-payments/any-id", nil},
		{http.MethodPut, "/billpiggy/api/v1/scheduled-payments/any-id", rentBody(start)},
		{http.MethodDelete, "/billpiggy/api/v1/scheduled-payments/any-id", nil},
	} {
		if response := app.do(t, testCase.method, testCase.path, "", testCase.body, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", testCase.method, testCase.path, response.Code)
		}
	}
}

// TestE2EScheduledPaymentsAreIsolatedBetweenUsers is the cross-tenant check
// for the new resource, matching the isolation the other resources already
// enforce in security_test.go.
func TestE2EScheduledPaymentsAreIsolatedBetweenUsers(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	outsiderToken := app.inviteAndLogin(t, adminToken, "outsider@example.com", "member", "outsider-password-1")
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	ownerToken := app.inviteAndLogin(t, adminToken, "owner@example.com", "member", "owner-password-1")
	var created apiScheduledPayment
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", ownerToken, rentBody(start), &created); response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/scheduled-payments/"+created.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider get status = %d, want 404", response.Code)
	}
	if response := app.do(t, http.MethodPut, "/billpiggy/api/v1/scheduled-payments/"+created.ID, outsiderToken, rentBody(start), nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider update status = %d, want 404", response.Code)
	}
	if response := app.do(t, http.MethodDelete, "/billpiggy/api/v1/scheduled-payments/"+created.ID, outsiderToken, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("outsider delete status = %d, want 404", response.Code)
	}
	var listed []apiScheduledPayment
	if response := app.do(t, http.MethodGet, "/billpiggy/api/v1/scheduled-payments/", outsiderToken, nil, &listed); response.Code != http.StatusOK {
		t.Fatalf("outsider list status = %d", response.Code)
	}
	if len(listed) != 0 {
		t.Fatalf("outsider saw %d payments, want 0", len(listed))
	}
}

func TestE2EScheduledPaymentRejectsAnInvalidFrequency(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	body := rentBody(time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC))
	body["frequency"] = "fortnightly"
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", adminToken, body, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unsupported frequency", response.Code)
	}
}

// TestE2EScheduledPaymentCustomFrequencyNeedsAnInterval pins the pairing the
// API requires: custom is the one frequency that cannot be scheduled without
// an explicit interval.
func TestE2EScheduledPaymentCustomFrequencyNeedsAnInterval(t *testing.T) {
	app := newE2EApp(t, "")
	adminToken, _ := app.loginAsAdmin(t)
	start := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)

	body := rentBody(start)
	body["frequency"] = "custom"
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", adminToken, body, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without custom_interval_days", response.Code)
	}

	body["custom_interval_days"] = 14
	var created apiScheduledPayment
	if response := app.do(t, http.MethodPost, "/billpiggy/api/v1/scheduled-payments/", adminToken, body, &created); response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 with an interval, body = %s", response.Code, response.Body.String())
	}
	if created.CustomIntervalDays != 14 || created.Frequency != "custom" {
		t.Fatalf("created = %#v, want a 14-day custom schedule", created)
	}
}
