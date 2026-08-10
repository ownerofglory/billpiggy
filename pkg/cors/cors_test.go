package cors_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/cors"
)

func TestParseOrigins(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, raw string
		want      []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "single", raw: "https://app.example.com", want: []string{"https://app.example.com"}},
		{name: "multiple with spaces", raw: "https://a.example.com, https://b.example.com", want: []string{"https://a.example.com", "https://b.example.com"}},
		{name: "stray commas", raw: "https://a.example.com,,", want: []string{"https://a.example.com"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := cors.ParseOrigins(test.raw)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseOrigins(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	t.Parallel()
	handler := cors.Middleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("preflight from allowed origin succeeds without reaching the handler", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodOptions, "/expenses", nil)
		request.Header.Set("Origin", "https://app.example.com")
		request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", response.Code, http.StatusNoContent)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Access-Control-Allow-Origin = %q, want the allowed origin", got)
		}
		if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\"", got)
		}
	})

	t.Run("preflight from disallowed origin gets no CORS headers", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodOptions, "/expenses", nil)
		request.Header.Set("Origin", "https://evil.example.com")
		request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", got)
		}
	})

	t.Run("actual request from allowed origin reaches the handler", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/expenses", nil)
		request.Header.Set("Origin", "https://app.example.com")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d (handler should have run)", response.Code, http.StatusNoContent)
		}
		if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\"", got)
		}
	})
}
