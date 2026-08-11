package openai_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestTranscribeReturnsTextAndUsage(t *testing.T) {
	t.Parallel()
	var capturedContentType string
	var capturedBody []byte
	url := newServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read multipart body: %v", err)
		}
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"we spent 25 euro at the cinema","usage":{"type":"tokens","input_tokens":5,"output_tokens":3,"total_tokens":8}}`)
	})
	client := newClient(t, url)

	result, err := client.Transcribe(context.Background(), domain.TranscriptionRequest{
		UserID: "owner-1", Audio: strings.NewReader("fake-audio-bytes"), Filename: "note.m4a", ContentType: "audio/m4a",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if result.Text != "we spent 25 euro at the cinema" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Usage.InputTokens != 5 || result.Usage.OutputTokens != 3 || result.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if !strings.HasPrefix(capturedContentType, "multipart/form-data") {
		t.Fatalf("content type = %q, want a multipart upload", capturedContentType)
	}
	if !strings.Contains(string(capturedBody), "note.m4a") {
		t.Fatal("uploaded filename did not reach the request")
	}
	if !strings.Contains(string(capturedBody), "fake-audio-bytes") {
		t.Fatal("audio bytes did not reach the request")
	}
}

func TestTranscribeCorrectsFilenameExtensionFromContentType(t *testing.T) {
	t.Parallel()
	var capturedBody []byte
	url := newServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read multipart body: %v", err)
		}
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"ok"}`)
	})
	client := newClient(t, url)

	// Safari on iOS records audio/mp4, but browsers commonly upload the
	// recorded blob under a generic or mismatched filename. OpenAI infers
	// the codec from the extension, so the request must carry one that
	// matches the actual content type rather than whatever the client sent.
	if _, err := client.Transcribe(context.Background(), domain.TranscriptionRequest{
		UserID: "owner-1", Audio: strings.NewReader("fake-audio-bytes"), Filename: "recording.webm", ContentType: "audio/mp4",
	}); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(string(capturedBody), "recording.mp4") {
		t.Fatalf("filename extension was not corrected to match audio/mp4: %s", capturedBody)
	}
	if strings.Contains(string(capturedBody), "recording.webm") {
		t.Fatalf("stale .webm extension still present: %s", capturedBody)
	}
}

func TestTranscribeRequiresAudio(t *testing.T) {
	t.Parallel()
	client := newClient(t, "http://127.0.0.1:1")
	if _, err := client.Transcribe(context.Background(), domain.TranscriptionRequest{UserID: "owner-1"}); err == nil {
		t.Fatal("expected an error for a request with no audio")
	}
}

func TestTranscribeSurfacesProviderErrors(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"unsupported audio format"}}`)
	})
	client := newClient(t, url)
	_, err := client.Transcribe(context.Background(), domain.TranscriptionRequest{
		UserID: "owner-1", Audio: strings.NewReader("bad-audio"), Filename: "note.xyz",
	})
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if !strings.Contains(err.Error(), "openai transcription") {
		t.Fatalf("error not wrapped with context: %v", err)
	}
}

func TestTranscribeDefaultsTheModelWhenUnset(t *testing.T) {
	t.Parallel()
	var capturedBody []byte
	url := newServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"ok"}`)
	})
	client := newClient(t, url)
	if _, err := client.Transcribe(context.Background(), domain.TranscriptionRequest{
		UserID: "owner-1", Audio: strings.NewReader("audio"), Filename: "a.m4a",
	}); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(string(capturedBody), "gpt-4o-mini-transcribe") {
		t.Fatalf("request did not carry the default transcription model: %s", capturedBody)
	}
}
