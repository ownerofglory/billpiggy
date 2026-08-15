package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
)

// poisonHandler fails every delivery until told otherwise, so a test can
// manufacture the dead letter these endpoints exist to recover from.
type poisonHandler struct{ failWith error }

func (h *poisonHandler) Name() string             { return "analytics_rollups" }
func (h *poisonHandler) AggregateTypes() []string { return []string{"expense"} }
func (h *poisonHandler) Handle(context.Context, outbox.Message) error {
	return h.failWith
}

// newAdminOutboxHarness builds a router with one dead-lettered delivery and a
// second event for the same aggregate stuck behind it — the exact shape that
// took production down.
func newAdminOutboxHarness(t *testing.T) (chi.Router, *memory.EventStore, string, string) {
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

	store := memory.NewEventStore()
	projection := &poisonHandler{failWith: errors.New("expense has no category")}
	if err := store.EnsureSubscription(context.Background(), projection.Name()); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	for _, eventType := range []string{"expense_added", "expense_updated"} {
		if err := store.Append(context.Background(), outbound.DomainEvent{
			ID: eventType + "-1", AggregateType: "expense", AggregateID: "expense-1", EventType: eventType,
			Payload: map[string]string{"expense_id": "expense-1"}, OccurredAt: time.Now().UnixMilli(), ActorID: admin.ID,
		}); err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
	}
	engine, err := outbox.NewEngine(store, projection, outbox.Options{
		Policy: outbox.Policy{MaxAttempts: 1, BaseBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond, LeaseTTL: time.Minute},
	})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	result, err := engine.Step(context.Background())
	if err != nil || result.Status != outbox.DeadLettered {
		t.Fatalf("step = %s, %v; want dead-lettered", result.Status, err)
	}

	outboxAdmin, err := service.NewOutboxAdminService(store)
	if err != nil {
		t.Fatalf("build outbox admin service: %v", err)
	}
	router := chi.NewRouter()
	handler.RegisterAdminOutboxRoutes(router, outboxAdmin, handler.NewAuthMiddleware(authService))
	return router, store, adminSession.AccessToken, memberSession.AccessToken
}

// TestListDeadLettersSurfacesTheFailureCause is the whole point of the
// endpoint: a stalled projection logs nothing once its messages stop being
// claimable, so last_error in the database is the only remaining record of
// why it stopped.
func TestListDeadLettersSurfacesTheFailureCause(t *testing.T) {
	router, _, adminToken, _ := newAdminOutboxHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/admin/outbox/dead-letters", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body)
	}
	var letters []domain.DeadLetter
	if err := json.NewDecoder(response.Body).Decode(&letters); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("returned %d dead letters, want 1", len(letters))
	}
	if letters[0].LastError != "expense has no category" {
		t.Fatalf("LastError = %q, want the handler failure that abandoned the delivery", letters[0].LastError)
	}
	if letters[0].Subscription != "analytics_rollups" || letters[0].AggregateID != "expense-1" {
		t.Fatalf("dead letter identifies %s/%s, want analytics_rollups/expense-1", letters[0].Subscription, letters[0].AggregateID)
	}
	// The blast radius matters as much as the cause: this is how many later
	// events for the same expense can never be projected until it is resolved.
	if letters[0].BlockedCount != 1 {
		t.Fatalf("BlockedCount = %d, want 1 later event held behind it", letters[0].BlockedCount)
	}
}

// TestRequeueDeadLetterReleasesTheMessagesBehindIt proves the endpoint is a
// real recovery path, not just an inspection one.
func TestRequeueDeadLetterReleasesTheMessagesBehindIt(t *testing.T) {
	router, store, adminToken, _ := newAdminOutboxHarness(t)
	if lag, err := store.Lag(context.Background(), "analytics_rollups"); err != nil || lag != 0 {
		t.Fatalf("lag = %d, %v; want 0 while everything is stuck behind the dead letter", lag, err)
	}

	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/admin/outbox/dead-letters/analytics_rollups:1/requeue", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body)
	}

	// Both the requeued delivery and the one it was blocking are deliverable
	// again, so the projection can catch up without a redeploy.
	lag, err := store.Lag(context.Background(), "analytics_rollups")
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	if lag != 2 {
		t.Fatalf("lag = %d, want 2 deliverable messages after requeuing", lag)
	}
}

func TestRequeueUnknownDeadLetterReports404(t *testing.T) {
	router, _, adminToken, _ := newAdminOutboxHarness(t)
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/admin/outbox/dead-letters/analytics_rollups:999/requeue", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

// TestDeadLetterEndpointsAllowSuperAdminOnly keeps an operational endpoint
// that exposes raw event metadata and can replay deliveries off limits to
// ordinary members.
func TestDeadLetterEndpointsAllowSuperAdminOnly(t *testing.T) {
	router, _, _, memberToken := newAdminOutboxHarness(t)
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/billpiggy/api/v1/admin/outbox/dead-letters"},
		{http.MethodPost, "/billpiggy/api/v1/admin/outbox/dead-letters/analytics_rollups:1/requeue"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("Authorization", "Bearer "+memberToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403 for a member", test.method, test.path, response.Code)
		}
	}
}
