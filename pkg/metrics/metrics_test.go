package metrics_test

import (
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/metrics"
)

func TestCounterVecRendersLabelsAndValues(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	counter := registry.NewCounterVec("billpiggy_http_requests_total", "Total HTTP requests.", "method", "status")
	counter.WithLabelValues("GET", "200").Inc()
	counter.WithLabelValues("GET", "200").Inc()
	counter.WithLabelValues("POST", "500").Add(3)

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "# HELP billpiggy_http_requests_total Total HTTP requests.") {
		t.Fatalf("missing HELP line: %s", output)
	}
	if !strings.Contains(output, "# TYPE billpiggy_http_requests_total counter") {
		t.Fatalf("missing TYPE line: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_requests_total{method="GET",status="200"} 2`) {
		t.Fatalf("missing or wrong GET/200 sample: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_requests_total{method="POST",status="500"} 3`) {
		t.Fatalf("missing or wrong POST/500 sample: %s", output)
	}
}

func TestHistogramVecRendersBucketsSumAndCount(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	histogram := registry.NewHistogramVec("billpiggy_http_request_duration_seconds", "Request latency.", []float64{0.1, 0.5, 1}, "route")
	observer := histogram.WithLabelValues("/expenses")
	observer.Observe(0.05)
	observer.Observe(0.2)
	observer.Observe(2)

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_bucket{le="0.1",route="/expenses"} 1`) {
		t.Fatalf("bucket le=0.1 wrong: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_bucket{le="0.5",route="/expenses"} 2`) {
		t.Fatalf("bucket le=0.5 wrong: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_bucket{le="1",route="/expenses"} 2`) {
		t.Fatalf("bucket le=1 wrong: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_bucket{le="+Inf",route="/expenses"} 3`) {
		t.Fatalf("bucket le=+Inf wrong: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_sum{route="/expenses"} 2.25`) {
		t.Fatalf("sum wrong: %s", output)
	}
	if !strings.Contains(output, `billpiggy_http_request_duration_seconds_count{route="/expenses"} 3`) {
		t.Fatalf("count wrong: %s", output)
	}
}

func TestCounterVecWithNoLabelsRendersBareName(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	counter := registry.NewCounterVec("billpiggy_expenses_created_total", "Expenses created.")
	counter.WithLabelValues().Inc()

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "billpiggy_expenses_created_total 1\n") {
		t.Fatalf("unlabeled counter sample missing or malformed: %s", buf.String())
	}
}
