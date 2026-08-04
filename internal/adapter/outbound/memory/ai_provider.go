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
// whole message. WithScriptedRounds additionally lets a test script a tool-call
// round trip: the first request gets the tool calls, every request after gets
// the answer, mirroring how a real model resolves tools before answering.
type AIProvider struct {
	mu                sync.Mutex
	answer            string
	toolCalls         []domain.ToolCall
	err               error
	requests          []domain.CompletionRequest
	scripted          bool
	scriptedToolCalls []domain.ToolCall
	scriptedAnswer    string
}

// NewAIProvider creates a provider that always returns the given answer.
func NewAIProvider(answer string) *AIProvider {
	return &AIProvider{answer: answer}
}

// WithToolCalls makes every call request the given tools alongside its answer.
// Combined with a caller that keeps honouring tool calls, this never resolves
// to a final answer — useful for exercising a bounded-rounds guard.
func (p *AIProvider) WithToolCalls(calls ...domain.ToolCall) *AIProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolCalls = calls
	return p
}

// WithScriptedRounds makes the first request return toolCalls with no content,
// and every request after return answer, so a test can exercise a full
// tool-call round trip without a real model deciding when to stop.
func (p *AIProvider) WithScriptedRounds(toolCalls []domain.ToolCall, answer string) *AIProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scripted = true
	p.scriptedToolCalls = toolCalls
	p.scriptedAnswer = answer
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

// Complete returns the scripted answer, or the scripted tool calls on the
// first request when WithScriptedRounds was used.
func (p *AIProvider) Complete(_ context.Context, request domain.CompletionRequest) (domain.Completion, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	if p.err != nil {
		return domain.Completion{}, p.err
	}
	if p.scripted && len(p.requests) == 1 {
		return domain.Completion{
			ToolCalls:    append([]domain.ToolCall(nil), p.scriptedToolCalls...),
			FinishReason: "tool_calls",
		}, nil
	}
	answer := p.answer
	if p.scripted {
		answer = p.scriptedAnswer
	}
	return domain.Completion{
		Content:      answer,
		ToolCalls:    append([]domain.ToolCall(nil), p.toolCalls...),
		FinishReason: "stop",
		Usage:        domain.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

// Stream replays the scripted answer word by word, or the scripted tool calls
// as the terminal chunk of the first request when WithScriptedRounds was used.
func (p *AIProvider) Stream(ctx context.Context, request domain.CompletionRequest) (<-chan domain.CompletionChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	round := len(p.requests)
	answer, toolCalls, failure := p.answer, append([]domain.ToolCall(nil), p.toolCalls...), p.err
	scripted, scriptedToolCalls, scriptedAnswer := p.scripted, append([]domain.ToolCall(nil), p.scriptedToolCalls...), p.scriptedAnswer
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
		if scripted && round == 1 {
			send(domain.CompletionChunk{ToolCalls: scriptedToolCalls, FinishReason: "tool_calls", Done: true})
			return
		}
		finalAnswer := answer
		if scripted {
			finalAnswer = scriptedAnswer
		}
		for index, word := range strings.Fields(finalAnswer) {
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
