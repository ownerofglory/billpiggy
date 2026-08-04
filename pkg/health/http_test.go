package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/health"
	"github.com/ownerofglory/billpiggy/pkg/metrics"
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

func TestMetricsRendersBillpiggyUpAndRegisteredGauges(t *testing.T) {
	t.Parallel()
	registry := health.NewRegistry()
	registry.RegisterGauge(`billpiggy_outbox_lag{subscription="analytics"}`, "Pending events for a subscription.", func() float64 { return 4 })

	response := httptest.NewRecorder()
	registry.Metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if !strings.Contains(body, "billpiggy_up 1\n") {
		t.Fatalf("missing billpiggy_up: %s", body)
	}
	if !strings.Contains(body, "# HELP billpiggy_outbox_lag Pending events for a subscription.") {
		t.Fatalf("gauge HELP line names the base metric, not one label combination: %s", body)
	}
	if !strings.Contains(body, `billpiggy_outbox_lag{subscription="analytics"} 4`) {
		t.Fatalf("missing gauge sample: %s", body)
	}
}

func TestMetricsGroupsMultipleLabelValuesOfOneGaugeUnderOneHelpType(t *testing.T) {
	t.Parallel()
	registry := health.NewRegistry()
	registry.RegisterGauge(`billpiggy_outbox_lag{subscription="analytics"}`, "Pending events for a subscription.", func() float64 { return 1 })
	registry.RegisterGauge(`billpiggy_outbox_lag{subscription="audit"}`, "Pending events for a subscription.", func() float64 { return 2 })

	response := httptest.NewRecorder()
	registry.Metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if strings.Count(body, "# HELP billpiggy_outbox_lag ") != 1 {
		t.Fatalf("expected exactly one HELP line for billpiggy_outbox_lag, got: %s", body)
	}
	if strings.Count(body, "# TYPE billpiggy_outbox_lag ") != 1 {
		t.Fatalf("expected exactly one TYPE line for billpiggy_outbox_lag, got: %s", body)
	}
	if !strings.Contains(body, `billpiggy_outbox_lag{subscription="analytics"} 1`) || !strings.Contains(body, `billpiggy_outbox_lag{subscription="audit"} 2`) {
		t.Fatalf("missing a gauge sample: %s", body)
	}
}

func TestMetricsRendersAttachedMetricsRegistry(t *testing.T) {
	t.Parallel()
	registry := health.NewRegistry()
	metricsRegistry := metrics.NewRegistry()
	counter := metricsRegistry.NewCounterVec("billpiggy_expenses_created_total", "Expenses created.")
	counter.WithLabelValues().Inc()
	registry.WithMetrics(metricsRegistry)

	response := httptest.NewRecorder()
	registry.Metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), "billpiggy_expenses_created_total 1") {
		t.Fatalf("attached metrics registry not rendered: %s", response.Body.String())
	}
}

func assertStatus(t *testing.T, handler http.Handler, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != want {
		t.Fatalf("status = %d, want %d", response.Code, want)
	}
}
