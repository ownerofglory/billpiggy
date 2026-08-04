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
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// assistantRouter wires the assistant endpoint behind real authentication and
// returns the router, a bearer token for the bootstrap admin, and the auth
// service so a test can change that admin's profile (such as the AI opt-out).
func assistantRouter(t *testing.T, assistant inbound.AssistantService) (chi.Router, string, *service.AuthService) {
	t.Helper()
	repository := memory.NewIdentityRepository()
	authService, err := service.NewAuthService(repository, service.AuthConfig{
		JWTSecret:                   "01234567890123456789012345678901",
		BootstrapSuperAdminEmail:    "admin@example.com",
		BootstrapSuperAdminPassword: "super-admin-password",
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
	router := chi.NewRouter()
	handler.RegisterAssistantRoutes(router, assistant, authService, handler.NewAuthMiddleware(authService))
	return router, session.AccessToken, authService
}

// sseEvents splits an SSE body into (event, data) pairs.
func sseEvents(t *testing.T, body string) [][2]string {
	t.Helper()
	events := make([][2]string, 0)
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var name, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if name != "" {
			events = append(events, [2]string{name, data})
		}
	}
	return events
}

func TestAssistantChatStreamsDeltasProgressively(t *testing.T) {
	expenses := memory.NewExpenseRepository()
	assistant, err := service.NewAssistantService(memory.NewAIProvider("You spent 25 euro on cinema."), expenses, memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	router, token, _ := assistantRouter(t, assistant)

	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{"message":"what did I spend?"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", contentType)
	}

	events := sseEvents(t, response.Body.String())
	if len(events) < 4 {
		t.Fatalf("got %d events, want started, several deltas and completed: %#v", len(events), events)
	}
	if events[0][0] != "message.started" {
		t.Fatalf("first event = %q, want message.started", events[0][0])
	}
	if events[len(events)-1][0] != "message.completed" {
		t.Fatalf("last event = %q, want message.completed", events[len(events)-1][0])
	}

	// The point of the refactor: the answer arrives as several deltas rather
	// than one buffered blob.
	var assembled strings.Builder
	deltas := 0
	for _, event := range events {
		if event[0] != "message.delta" {
			continue
		}
		deltas++
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(event[1]), &payload); err != nil {
			t.Fatalf("decode delta: %v", err)
		}
		assembled.WriteString(payload.Delta)
	}
	if deltas < 2 {
		t.Fatalf("received %d delta events; the stream must be incremental", deltas)
	}
	if assembled.String() != "You spent 25 euro on cinema." {
		t.Fatalf("assembled %q", assembled.String())
	}
}

func TestAssistantChatReportsRateLimitOnTheStream(t *testing.T) {
	assistant, err := service.NewAssistantService(memory.NewAIProvider("ok"), memory.NewExpenseRepository(), memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	router, token, _ := assistantRouter(t, assistant)

	var lastBody string
	for i := 0; i < 11; i++ {
		request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{"message":"hi"}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		lastBody = response.Body.String()
	}
	if !strings.Contains(lastBody, "rate_limited") {
		t.Fatalf("11th request did not report the rate limit: %s", lastBody)
	}
}

func TestAssistantChatReportsAnUnconfiguredProvider(t *testing.T) {
	// Without OPENAI_API_KEY the service is nil; the endpoint must still honour
	// its SSE contract rather than failing the request outright.
	router, token, _ := assistantRouter(t, nil)
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{"message":"hi"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "assistant_not_configured") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestAssistantChatRequiresAuthentication(t *testing.T) {
	assistant, err := service.NewAssistantService(memory.NewAIProvider("ok"), memory.NewExpenseRepository(), memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	router, _, _ := assistantRouter(t, assistant)
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{"message":"hi"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", response.Code)
	}
}

func TestAssistantChatReportsAnOptedOutUser(t *testing.T) {
	assistant, err := service.NewAssistantService(memory.NewAIProvider("ok"), memory.NewExpenseRepository(), memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	router, token, authService := assistantRouter(t, assistant)
	profile, err := authService.GetProfile(context.Background(), mustSubject(t, authService, token))
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if _, err := authService.UpdateProfile(context.Background(), profile.ID, profile.DisplayName, profile.Email, profile.EmailNotificationsEnabled, false); err != nil {
		t.Fatalf("disable AI: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{"message":"hi"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	// The opt-out is checked before the response commits to being a stream,
	// same as a malformed body.
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an opted-out user", response.Code)
	}
}

// mustSubject decodes the user id out of an access token via the auth
// service that issued it.
func mustSubject(t *testing.T, authService *service.AuthService, accessToken string) string {
	t.Helper()
	user, err := authService.AuthenticateAccessToken(context.Background(), accessToken)
	if err != nil {
		t.Fatalf("authenticate token: %v", err)
	}
	return user.ID
}

func TestAssistantChatRejectsAMalformedBody(t *testing.T) {
	assistant, err := service.NewAssistantService(memory.NewAIProvider("ok"), memory.NewExpenseRepository(), memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	router, token, _ := assistantRouter(t, assistant)
	request := httptest.NewRequest(http.MethodPost, "/billpiggy/api/v1/assistant/chat", strings.NewReader(`{`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	// A bad body is answered as an ordinary JSON error, before the response
	// commits to being a stream.
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want JSON", contentType)
	}
}
