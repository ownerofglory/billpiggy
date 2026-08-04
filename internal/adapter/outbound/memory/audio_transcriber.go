package memory

import (
	"context"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AudioTranscriber is a scripted transcriber for local development and tests.
type AudioTranscriber struct {
	mu       sync.Mutex
	text     string
	err      error
	requests []domain.TranscriptionRequest
}

// NewAudioTranscriber creates a transcriber that always returns text.
func NewAudioTranscriber(text string) *AudioTranscriber {
	return &AudioTranscriber{text: text}
}

// WithError makes every call fail, for exercising error paths.
func (t *AudioTranscriber) WithError(err error) *AudioTranscriber {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.err = err
	return t
}

// Requests returns the requests the transcriber received, for test assertions.
func (t *AudioTranscriber) Requests() []domain.TranscriptionRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]domain.TranscriptionRequest(nil), t.requests...)
}

// Transcribe returns the scripted text.
func (t *AudioTranscriber) Transcribe(_ context.Context, request domain.TranscriptionRequest) (domain.Transcription, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests = append(t.requests, request)
	if t.err != nil {
		return domain.Transcription{}, t.err
	}
	return domain.Transcription{Text: t.text, Usage: domain.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}}, nil
}
