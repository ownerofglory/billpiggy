package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMailerSendSenderRejectsMissingConfig(t *testing.T) {
	t.Parallel()
	if _, err := NewMailerSendSender("", "from@example.com", ""); err == nil {
		t.Fatal("expected an error for a missing API key")
	}
	if _, err := NewMailerSendSender("key", "", ""); err == nil {
		t.Fatal("expected an error for a missing from address")
	}
}

func TestMailerSendSenderPostsTheDocumentedRequestShape(t *testing.T) {
	t.Parallel()
	var gotAuth, gotContentType, gotMethod string
	var body mailerSendRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("X-Message-Id", "msg-123")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sender, err := NewMailerSendSender("test-api-key", "billpiggy@example.com", "BillPiggy")
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	sender.endpoint = server.URL

	if err := sender.Send(context.Background(), "user@example.com", "Hello", "plain body", "<p>html body</p>"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer test-api-key" {
		t.Fatalf("authorization header = %q, want Bearer test-api-key", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotContentType)
	}
	if body.From.Email != "billpiggy@example.com" || body.From.Name != "BillPiggy" {
		t.Fatalf("from = %#v", body.From)
	}
	if len(body.To) != 1 || body.To[0].Email != "user@example.com" {
		t.Fatalf("to = %#v", body.To)
	}
	if body.Subject != "Hello" || body.Text != "plain body" || body.HTML != "<p>html body</p>" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestMailerSendSenderReturnsTheValidationErrorMessage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"The given data was invalid.","errors":{"from.email":["The from.email must be a valid email address. #MS42207"]}}`))
	}))
	defer server.Close()

	sender, err := NewMailerSendSender("test-api-key", "not-an-email", "")
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	sender.endpoint = server.URL

	err = sender.Send(context.Background(), "user@example.com", "Hello", "text", "<p>html</p>")
	if err == nil {
		t.Fatal("expected an error for a 422 response")
	}
	if !strings.Contains(err.Error(), "The given data was invalid.") || !strings.Contains(err.Error(), "from.email") {
		t.Fatalf("error = %q, want it to surface the validation message and field", err.Error())
	}
}

func TestMailerSendSenderFallsBackToRawBodyForANonJSONError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer server.Close()

	sender, err := NewMailerSendSender("test-api-key", "billpiggy@example.com", "")
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	sender.endpoint = server.URL

	err = sender.Send(context.Background(), "user@example.com", "Hello", "text", "<p>html</p>")
	if err == nil {
		t.Fatal("expected an error for a 502 response")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %q, want the status code preserved", err.Error())
	}
}

func TestMailerSendSenderRejectsCancelledContext(t *testing.T) {
	t.Parallel()
	sender, err := NewMailerSendSender("test-api-key", "billpiggy@example.com", "")
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, "user@example.com", "subject", "text", "html"); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}
