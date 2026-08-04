package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// HTTPMiddleware records one request count and one latency observation per
// request, labeled by route, method, and status.
//
// route is read from chi's RouteContext after the handler chain returns, so
// it reports the matched pattern (e.g. "/expenses/{expenseID}") rather than
// the literal path — the literal path would give every distinct expense ID
// its own label series, which is exactly the unbounded-cardinality mistake
// route-pattern labeling avoids. routePattern is injected rather than
// imported directly so this package stays framework-agnostic.
func HTTPMiddleware(requests *CounterVec, latency *HistogramVec, routePattern func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)

			route := routePattern(r)
			if route == "" {
				route = "unmatched"
			}
			status := strconv.Itoa(recorder.status)
			requests.WithLabelValues(route, r.Method, status).Inc()
			latency.WithLabelValues(route, r.Method, status).Observe(time.Since(start).Seconds())
		})
	}
}

// statusRecorder captures the status code a handler wrote, defaulting to 200
// since a handler that never calls WriteHeader implicitly sends one.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
