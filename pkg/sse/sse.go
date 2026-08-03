// Package sse provides small, reusable Server-Sent Events helpers.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Write emits and flushes one named SSE event.
func Write(w http.ResponseWriter, event string, value any) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming is unsupported")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// Prepare sets the HTTP headers required for an SSE response.
func Prepare(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}
