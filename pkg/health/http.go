// Package health provides dependency-aware HTTP probes for long-running services.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/ownerofglory/billpiggy/pkg/metrics"
)

// Check verifies whether a required runtime component is usable.
type Check func(context.Context) error

// gaugeFunc is a single-value gauge evaluated at scrape time, for pull-based
// values such as an outbox subscription's lag or a worker's last-run time
// that are naturally computed on demand rather than pushed to.
type gaugeFunc struct {
	help string
	fn   func() float64
}

// Registry holds the readiness checks and pull-based gauges registered by
// outbound adapters and background workers.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Check
	gauges  map[string]gaugeFunc
	metrics *metrics.Registry
	started bool
}

// NewRegistry creates an empty health registry. Call MarkStarted after bootstrap and migrations finish.
func NewRegistry() *Registry {
	return &Registry{checks: make(map[string]Check), gauges: make(map[string]gaugeFunc)}
}

// Register adds a named required component check.
func (r *Registry) Register(name string, check Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = check
}

// RegisterGauge adds a named pull-based gauge, rendered by Metrics alongside
// billpiggy_up. name should be the full Prometheus sample line's metric
// identifier, including any inline labels the caller wants, e.g.
// `billpiggy_outbox_lag{subscription="analytics"}`.
func (r *Registry) RegisterGauge(name, help string, fn func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[name] = gaugeFunc{help: help, fn: fn}
}

// WithMetrics attaches a metrics.Registry whose counters and histograms are
// rendered alongside gauges and billpiggy_up.
func (r *Registry) WithMetrics(registry *metrics.Registry) *Registry {
	r.metrics = registry
	return r
}

// MarkStarted records that application initialization completed successfully.
func (r *Registry) MarkStarted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = true
}

// Live responds once the process can serve HTTP.
func (r *Registry) Live(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok", nil)
}

// Ready verifies every required dependency.
func (r *Registry) Ready(w http.ResponseWriter, request *http.Request) {
	r.mu.RLock()
	checks := make(map[string]Check, len(r.checks))
	for name, check := range r.checks {
		checks[name] = check
	}
	r.mu.RUnlock()
	failed := make([]string, 0)
	for name, check := range checks {
		if err := check(request.Context()); err != nil {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		writeStatus(w, http.StatusServiceUnavailable, "unavailable", failed)
		return
	}
	writeStatus(w, http.StatusOK, "ok", nil)
}

// Startup prevents traffic before initialization and the first readiness check complete.
func (r *Registry) Startup(w http.ResponseWriter, request *http.Request) {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()
	if !started {
		writeStatus(w, http.StatusServiceUnavailable, "starting", nil)
		return
	}
	r.Ready(w, request)
}

// Metrics emits billpiggy_up, every registered gauge, and — when WithMetrics
// was called — every counter and histogram from the attached metrics.Registry,
// all in Prometheus text exposition format.
func (r *Registry) Metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# HELP billpiggy_up Whether the BillPiggy process can serve HTTP.\n# TYPE billpiggy_up gauge\nbillpiggy_up 1\n"))

	r.mu.RLock()
	names := make([]string, 0, len(r.gauges))
	for name := range r.gauges {
		names = append(names, name)
	}
	gauges := r.gauges
	metricsRegistry := r.metrics
	r.mu.RUnlock()
	// Sorting the full names (base name plus any inline labels) also groups
	// every label combination of one gauge family together, since they share
	// the base name as a prefix — so lastBase tracking below only ever needs
	// to compare against the immediately preceding line.
	sort.Strings(names)
	lastBase := ""
	for _, name := range names {
		gauge := gauges[name]
		base := baseMetricName(name)
		if base != lastBase {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", base, gauge.help, base)
			lastBase = base
		}
		fmt.Fprintf(w, "%s %g\n", name, gauge.fn())
	}
	if metricsRegistry != nil {
		_ = metricsRegistry.Render(w)
	}
}

// baseMetricName strips an inline label block from a gauge's full sample
// name, since HELP/TYPE lines name the metric family, not one label
// combination.
func baseMetricName(name string) string {
	if i := strings.IndexByte(name, '{'); i >= 0 {
		return name[:i]
	}
	return name
}

func writeStatus(w http.ResponseWriter, status int, value string, failed []string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": value, "failed": failed})
}
