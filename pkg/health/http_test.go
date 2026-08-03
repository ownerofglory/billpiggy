package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/health"
)

func TestRegistryProbes(t *testing.T) {
	t.Parallel()
	registry := health.NewRegistry()
	registry.Register("database", func(_ context.Context) error { return errors.New("unavailable") })
	assertStatus(t, http.HandlerFunc(registry.Live), http.StatusOK)
	assertStatus(t, http.HandlerFunc(registry.Startup), http.StatusServiceUnavailable)
	assertStatus(t, http.HandlerFunc(registry.Ready), http.StatusServiceUnavailable)
}

func TestRegistryStartupAfterHealthyChecks(t *testing.T) {
	t.Parallel()
	registry := health.NewRegistry()
	registry.Register("database", func(_ context.Context) error { return nil })
	registry.MarkStarted()
	assertStatus(t, http.HandlerFunc(registry.Startup), http.StatusOK)
}

func assertStatus(t *testing.T, handler http.Handler, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != want {
		t.Fatalf("status = %d, want %d", response.Code, want)
	}
}
