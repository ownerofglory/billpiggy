package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/metrics"
)

func TestHTTPMiddlewareRecordsCountAndLatencyByRouteMethodStatus(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	requests := registry.NewCounterVec("billpiggy_http_requests_total", "Total HTTP requests.", "route", "method", "status")
	latency := registry.NewHistogramVec("billpiggy_http_request_duration_seconds", "Request latency.", metrics.DefaultLatencyBuckets, "route", "method", "status")

	handler := metrics.HTTPMiddleware(requests, latency, func(*http.Request) string { return "/expenses/{expenseID}" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }),
	)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/expenses/abc-123", nil))

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, `billpiggy_http_requests_total{route="/expenses/{expenseID}",method="POST",status="201"} 1`) {
		t.Fatalf("missing labeled request count: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_count{route="/expenses/{expenseID}",method="POST",status="201"} 1`) {
		t.Fatalf("missing labeled latency observation: %s", output)
	}
}

func TestHTTPMiddlewareDefaultsStatusToOKWhenHandlerNeverWritesHeader(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	requests := registry.NewCounterVec("billpiggy_http_requests_total", "Total HTTP requests.", "route", "method", "status")
	latency := registry.NewHistogramVec("billpiggy_http_request_duration_seconds", "Request latency.", metrics.DefaultLatencyBuckets, "route", "method", "status")

	handler := metrics.HTTPMiddleware(requests, latency, func(*http.Request) string { return "/livez" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }),
	)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/livez", nil))

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `status="200"`) {
		t.Fatalf("expected default status 200: %s", buf.String())
	}
}

func TestHTTPMiddlewarePreservesFlusherForStreamingHandlers(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	requests := registry.NewCounterVec("billpiggy_http_requests_total", "Total HTTP requests.", "route", "method", "status")
	latency := registry.NewHistogramVec("billpiggy_http_request_duration_seconds", "Request latency.", metrics.DefaultLatencyBuckets, "route", "method", "status")

	handler := metrics.HTTPMiddleware(requests, latency, func(*http.Request) string { return "/assistant/chat" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("response writer wrapped by HTTPMiddleware must still implement http.Flusher")
				return
			}
			_, _ = w.Write([]byte("event: message.delta\ndata: hi\n\n"))
			flusher.Flush()
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/assistant/chat", nil))
	if response.Body.String() == "" {
		t.Fatal("expected the streamed body to have been written")
	}
	if !response.Flushed {
		t.Fatal("expected Flush to have reached the underlying recorder")
	}
}
