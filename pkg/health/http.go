// Package health provides dependency-aware HTTP probes for long-running services.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

// Check verifies whether a required runtime component is usable.
type Check func(context.Context) error

// Registry holds the readiness checks registered by outbound adapters.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Check
	started bool
}

// NewRegistry creates an empty health registry. Call MarkStarted after bootstrap and migrations finish.
func NewRegistry() *Registry { return &Registry{checks: make(map[string]Check)} }

// Register adds a named required component check.
func (r *Registry) Register(name string, check Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = check
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

// Metrics emits a minimal Prometheus-compatible service metric. Instrumentation can add a registry later.
func (r *Registry) Metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# HELP billpiggy_up Whether the BillPiggy process can serve HTTP.\n# TYPE billpiggy_up gauge\nbillpiggy_up 1\n"))
}

func writeStatus(w http.ResponseWriter, status int, value string, failed []string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": value, "failed": failed})
}
