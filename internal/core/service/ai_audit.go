package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// AuditedAIProvider wraps an AIProvider and records every call to
// AIRequestRepository for cost tracking, regardless of which feature is
// asking. One instance is bound to one workload label; wrap the same
// underlying provider once per workload (assistant, receipt extraction,
// sentence extraction, transcription) rather than sharing one instance.
type AuditedAIProvider struct {
	provider outbound.AIProvider
	requests outbound.AIRequestRepository
	workload domain.AIWorkload
	ids      func() string
	now      func() time.Time
}

// NewAuditedAIProvider creates an auditing decorator around provider.
func NewAuditedAIProvider(provider outbound.AIProvider, requests outbound.AIRequestRepository, workload domain.AIWorkload) (*AuditedAIProvider, error) {
	if provider == nil || requests == nil {
		return nil, errors.New("AI provider and AI request repository are required")
	}
	return &AuditedAIProvider{provider: provider, requests: requests, workload: workload, ids: uuid.NewString, now: time.Now}, nil
}

// Complete calls the wrapped provider and records the outcome.
func (p *AuditedAIProvider) Complete(ctx context.Context, request domain.CompletionRequest) (domain.Completion, error) {
	start := p.now()
	completion, err := p.provider.Complete(ctx, request)
	p.record(ctx, request, completion.Usage, p.now().Sub(start), err)
	return completion, err
}

// Stream calls the wrapped provider and records the outcome once the stream
// finishes, using the usage the final chunk reports.
func (p *AuditedAIProvider) Stream(ctx context.Context, request domain.CompletionRequest) (<-chan domain.CompletionChunk, error) {
	chunks, err := p.provider.Stream(ctx, request)
	if err != nil {
		p.record(ctx, request, domain.TokenUsage{}, 0, err)
		return nil, err
	}
	start := p.now()
	audited := make(chan domain.CompletionChunk)
	go func() {
		defer close(audited)
		var usage domain.TokenUsage
		var streamErr error
		for chunk := range chunks {
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
			select {
			case audited <- chunk:
			case <-ctx.Done():
				return
			}
		}
		p.record(ctx, request, usage, p.now().Sub(start), streamErr)
	}()
	return audited, nil
}

// AuditedAudioTranscriber wraps an AudioTranscriber and records every call to
// AIRequestRepository under the transcription workload.
type AuditedAudioTranscriber struct {
	transcriber outbound.AudioTranscriber
	requests    outbound.AIRequestRepository
	ids         func() string
	now         func() time.Time
}

// NewAuditedAudioTranscriber creates an auditing decorator around transcriber.
func NewAuditedAudioTranscriber(transcriber outbound.AudioTranscriber, requests outbound.AIRequestRepository) (*AuditedAudioTranscriber, error) {
	if transcriber == nil || requests == nil {
		return nil, errors.New("audio transcriber and AI request repository are required")
	}
	return &AuditedAudioTranscriber{transcriber: transcriber, requests: requests, ids: uuid.NewString, now: time.Now}, nil
}

// Transcribe calls the wrapped transcriber and records the outcome.
func (t *AuditedAudioTranscriber) Transcribe(ctx context.Context, request domain.TranscriptionRequest) (domain.Transcription, error) {
	start := t.now()
	result, err := t.transcriber.Transcribe(ctx, request)
	outcome, message := domain.AIRequestSuccess, ""
	if err != nil {
		outcome, message = domain.AIRequestError, err.Error()
	}
	_ = t.requests.RecordRequest(ctx, domain.AIRequestRecord{
		ID: t.ids(), UserID: request.UserID, Workload: domain.AIWorkloadTranscription, Model: request.Model,
		Usage: result.Usage, LatencyMS: t.now().Sub(start).Milliseconds(), Outcome: outcome, ErrorMessage: message, CreatedAt: t.now(),
	})
	return result, err
}

// record writes one AI request audit entry. A recording failure is swallowed
// rather than surfaced: the AI answer has already succeeded or failed on its
// own terms, and core services deliberately have no logger to report through.
func (p *AuditedAIProvider) record(ctx context.Context, request domain.CompletionRequest, usage domain.TokenUsage, latency time.Duration, err error) {
	outcome, message := domain.AIRequestSuccess, ""
	if err != nil {
		outcome, message = domain.AIRequestError, err.Error()
	}
	_ = p.requests.RecordRequest(ctx, domain.AIRequestRecord{
		ID: p.ids(), UserID: request.UserID, Workload: p.workload, Model: request.Model,
		Usage: usage, LatencyMS: latency.Milliseconds(), Outcome: outcome, ErrorMessage: message, CreatedAt: p.now(),
	})
}
