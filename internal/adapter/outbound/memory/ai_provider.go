package memory

import (
	"context"
	"strings"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AIProvider is a scripted AI provider for local development and tests.
//
// It replays a fixed answer, splitting it into several chunks when streamed so
// callers that relay deltas are genuinely exercised rather than receiving one
// whole message.
type AIProvider struct {
	mu        sync.Mutex
	answer    string
	toolCalls []domain.ToolCall
	err       error
	requests  []domain.CompletionRequest
}

// NewAIProvider creates a provider that always returns the given answer.
func NewAIProvider(answer string) *AIProvider {
	return &AIProvider{answer: answer}
}

// WithToolCalls makes the provider request tools alongside its answer.
func (p *AIProvider) WithToolCalls(calls ...domain.ToolCall) *AIProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolCalls = calls
	return p
}

// WithError makes every call fail, for exercising error paths.
func (p *AIProvider) WithError(err error) *AIProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
	return p
}

// Requests returns the requests the provider received, for test assertions.
func (p *AIProvider) Requests() []domain.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.CompletionRequest(nil), p.requests...)
}

// Complete returns the scripted answer.
func (p *AIProvider) Complete(_ context.Context, request domain.CompletionRequest) (domain.Completion, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	if p.err != nil {
		return domain.Completion{}, p.err
	}
	return domain.Completion{
		Content:      p.answer,
		ToolCalls:    append([]domain.ToolCall(nil), p.toolCalls...),
		FinishReason: "stop",
		Usage:        domain.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

// Stream replays the scripted answer word by word.
func (p *AIProvider) Stream(ctx context.Context, request domain.CompletionRequest) (<-chan domain.CompletionChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	answer, toolCalls, failure := p.answer, append([]domain.ToolCall(nil), p.toolCalls...), p.err
	p.mu.Unlock()

	chunks := make(chan domain.CompletionChunk)
	go func() {
		defer close(chunks)
		send := func(chunk domain.CompletionChunk) bool {
			select {
			case chunks <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if failure != nil {
			send(domain.CompletionChunk{Err: failure})
			return
		}
		for index, word := range strings.Fields(answer) {
			delta := word
			if index > 0 {
				delta = " " + word
			}
			if !send(domain.CompletionChunk{ContentDelta: delta}) {
				return
			}
		}
		send(domain.CompletionChunk{ToolCalls: toolCalls, FinishReason: "stop", Done: true})
	}()
	return chunks, nil
}
