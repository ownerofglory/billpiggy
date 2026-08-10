// Package cors builds the credentialed cross-origin middleware the API
// needs, since the frontend is never served from this API's own origin.
//
// Implemented directly against net/http rather than a third-party CORS
// library: BillPiggy is pinned to Go 1.24 for its Docker build, and most
// third-party dependencies drag go.sum toward a newer toolchain requirement
// (see pkg/imageproc for the same tradeoff made deliberately elsewhere).
package cors

import (
	"net/http"
	"strconv"
	"strings"
)

const maxAgeSeconds = 600

var allowedMethods = strings.Join([]string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions,
}, ", ")

// Middleware allows credentialed requests (cookies, Authorization headers)
// from exactly the given origins, and answers preflight OPTIONS requests
// directly instead of leaving them to fall through to a 405. Origins must be
// listed explicitly rather than using a wildcard: the CORS spec forbids
// combining "*" with credentialed requests. An origin outside the allowlist
// never gets an Access-Control-Allow-Origin header — reflecting the request's
// Origin back unconditionally would defeat the allowlist entirely.
func Middleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			w.Header().Add("Vary", "Origin")
			if allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			// A preflight is specifically an OPTIONS request carrying this
			// header; a plain OPTIONS request without it should reach the
			// handler like any other method.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed[origin] {
					w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(maxAgeSeconds))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParseOrigins splits a comma-separated CORS_ALLOWED_ORIGINS value into a
// clean origin list, dropping empty entries left by stray commas or spaces.
func ParseOrigins(raw string) []string {
	var origins []string
	for _, origin := range strings.Split(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
