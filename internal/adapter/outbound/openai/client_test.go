package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/openai"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// capture records the request body a test server received.
type capture struct {
	body map[string]any
}

// newServer starts a stub OpenAI endpoint that replies with the given body.
func newServer(t *testing.T, recorded *capture, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recorded != nil {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			if err := json.Unmarshal(raw, &recorded.body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// jsonResponse replies with a static JSON body.
func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// sseResponse replies with a Server-Sent Events stream of the given data lines.
func sseResponse(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func newClient(t *testing.T, baseURL string, options ...openai.Option) *openai.Client {
	t.Helper()
	client, err := openai.NewClient("test-key", append([]openai.Option{openai.WithBaseURL(baseURL)}, options...)...)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	return client
}

func TestNewClientRequiresAnAPIKey(t *testing.T) {
	t.Parallel()
	if _, err := openai.NewClient(""); err == nil {
		t.Fatal("expected an error without an API key")
	}
}

func TestCompleteReturnsContentAndUsage(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{
		"id":"chatcmpl-1","object":"chat.completion","model":"gpt-5.6-luna",
		"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"You spent 25 euro."}}],
		"usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}}`))

	client := newClient(t, url, openai.WithModel("gpt-5.6-luna"))
	completion, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.SystemMessage("be brief"), domain.UserMessage("what did I spend?")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completion.Content != "You spent 25 euro." || completion.FinishReason != "stop" {
		t.Fatalf("unexpected completion %#v", completion)
	}
	if completion.Usage.InputTokens != 40 || completion.Usage.OutputTokens != 8 || completion.Usage.TotalTokens != 48 {
		t.Fatalf("usage not mapped: %#v", completion.Usage)
	}
	if recorded.body["model"] != "gpt-5.6-luna" {
		t.Fatalf("model = %v, want the configured default", recorded.body["model"])
	}
	messages, _ := recorded.body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("sent %d messages, want 2", len(messages))
	}
}

func TestCompleteRequestModelOverridesTheDefault(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	client := newClient(t, url, openai.WithModel("gpt-5.6-luna"))

	if _, err := client.Complete(context.Background(), domain.CompletionRequest{
		Model:    "gpt-4o-mini",
		Messages: []domain.Message{domain.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if recorded.body["model"] != "gpt-4o-mini" {
		t.Fatalf("model = %v, want the per-request override", recorded.body["model"])
	}
}

func TestCompleteRejectsAnEmptyConversation(t *testing.T) {
	t.Parallel()
	client := newClient(t, "http://127.0.0.1:1")
	if _, err := client.Complete(context.Background(), domain.CompletionRequest{}); err == nil {
		t.Fatal("expected an error for a request with no messages")
	}
}

func TestCompleteMergesConsecutiveToolCallMessagesIntoOneTurn(t *testing.T) {
	t.Parallel()
	// The Chat Completions API expects the tool calls one turn requested to
	// arrive together in a single assistant message, followed immediately by
	// their results — not interleaved assistant/tool/assistant/tool turns.
	// domain.Message models one call per message, so the adapter must merge
	// adjacent ones back into the wire format the API actually requires.
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"message":{"role":"assistant","content":"done"}}]}`))
	client := newClient(t, url)

	_, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{
			domain.UserMessage("what did I spend and what are my budgets?"),
			domain.AssistantToolCallMessage(domain.ToolCall{ID: "call_1", Name: "query_expenses", ArgsRaw: `{}`}),
			domain.AssistantToolCallMessage(domain.ToolCall{ID: "call_2", Name: "query_budgets", ArgsRaw: `{}`}),
			domain.ToolResultMessage("call_1", `{"expenses":[]}`),
			domain.ToolResultMessage("call_2", `{"budgets":[]}`),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	messages, _ := recorded.body["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("sent %d messages, want user, one merged assistant turn, and two tool results: %#v", len(messages), messages)
	}
	assistantTurn, _ := messages[1].(map[string]any)
	if assistantTurn["role"] != "assistant" {
		t.Fatalf("messages[1] role = %v, want assistant", assistantTurn["role"])
	}
	toolCalls, _ := assistantTurn["tool_calls"].([]any)
	if len(toolCalls) != 2 {
		t.Fatalf("merged assistant turn carries %d tool_calls, want both calls together: %#v", len(toolCalls), assistantTurn)
	}
	first, _ := toolCalls[0].(map[string]any)
	if first["id"] != "call_1" {
		t.Fatalf("first tool call = %#v, want call_1 preserved in order", first)
	}
	second, _ := toolCalls[1].(map[string]any)
	if second["id"] != "call_2" {
		t.Fatalf("second tool call = %#v, want call_2 preserved in order", second)
	}
	toolMessage1, _ := messages[2].(map[string]any)
	toolMessage2, _ := messages[3].(map[string]any)
	if toolMessage1["role"] != "tool" || toolMessage1["tool_call_id"] != "call_1" {
		t.Fatalf("messages[2] = %#v, want the call_1 result immediately after the merged turn", toolMessage1)
	}
	if toolMessage2["role"] != "tool" || toolMessage2["tool_call_id"] != "call_2" {
		t.Fatalf("messages[3] = %#v, want the call_2 result following it", toolMessage2)
	}
}

func TestCompleteSurfacesProviderErrors(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limit reached","type":"rate_limit_error"}}`)
	})
	client := newClient(t, url)
	_, err := client.Complete(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if !strings.Contains(err.Error(), "openai completion") {
		t.Fatalf("error not wrapped with context: %v", err)
	}
}

func TestCompleteReportsNoChoices(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, jsonResponse(`{"id":"chatcmpl-1","choices":[]}`))
	client := newClient(t, url)
	if _, err := client.Complete(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("hi")}}); err == nil {
		t.Fatal("expected an error when the provider returns no choices")
	}
}

func TestCompleteSendsToolsAndReadsToolCalls(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{
		"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"query_expenses","arguments":"{\"category\":\"Food\"}"}}]}}]}`))

	client := newClient(t, url)
	completion, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.UserMessage("what did I spend on food?")},
		Tools: []domain.Tool{{
			Name:        "query_expenses",
			Description: "Look up the owner's expenses",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"category": map[string]any{"type": "string"}}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(completion.ToolCalls))
	}
	call := completion.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "query_expenses" || call.ArgsRaw != `{"category":"Food"}` {
		t.Fatalf("unexpected tool call %#v", call)
	}

	tools, _ := recorded.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("sent %d tools, want 1", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool type = %v, want function", tool["type"])
	}
	function, _ := tool["function"].(map[string]any)
	if function["name"] != "query_expenses" {
		t.Fatalf("tool name = %v", function["name"])
	}
}

func TestCompleteForcesReasoningEffortOffWhenToolsArePresent(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))

	client := newClient(t, url)
	_, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.UserMessage("what did I spend on food?")},
		Tools: []domain.Tool{{
			Name:        "query_expenses",
			Description: "Look up the owner's expenses",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if got := recorded.body["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort = %v, want \"none\" when tools are attached", got)
	}
}

func TestCompleteOmitsReasoningEffortWithoutTools(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))

	client := newClient(t, url)
	_, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.UserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if _, present := recorded.body["reasoning_effort"]; present {
		t.Fatalf("reasoning_effort = %v, want the field omitted without tools", recorded.body["reasoning_effort"])
	}
}

func TestCompleteSendsStructuredOutputSchema(t *testing.T) {
	t.Parallel()
	type extractedExpense struct {
		Title       string `json:"title"`
		AmountMinor int64  `json:"amount_minor"`
	}
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"title\":\"Cinema\",\"amount_minor\":2500}"}}]}`))

	client := newClient(t, url)
	completion, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.UserMessage("we spent 25 euro at the cinema")},
		ResponseFormat: &domain.ResponseFormat{
			Name:        "expense",
			Description: "A single extracted expense",
			Schema:      domain.GenerateSchema[extractedExpense](),
			Strict:      true,
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var decoded extractedExpense
	if err := json.Unmarshal([]byte(completion.Content), &decoded); err != nil {
		t.Fatalf("structured content is not decodable: %v", err)
	}
	if decoded.Title != "Cinema" || decoded.AmountMinor != 2500 {
		t.Fatalf("unexpected structured result %#v", decoded)
	}

	format, _ := recorded.body["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %v, want json_schema", format["type"])
	}
	schema, _ := format["json_schema"].(map[string]any)
	if schema["name"] != "expense" || schema["strict"] != true {
		t.Fatalf("unexpected json_schema %#v", schema)
	}
}

func TestCompleteSendsImageContent(t *testing.T) {
	t.Parallel()
	recorded := &capture{}
	url := newServer(t, recorded, jsonResponse(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	client := newClient(t, url)

	// Receipt extraction passes the normalised image inline as a data URI, so
	// nothing has to be published to be read by the model.
	if _, err := client.Complete(context.Background(), domain.CompletionRequest{
		Messages: []domain.Message{domain.UserImageMessage("extract this receipt", "data:image/jpeg;base64,AAAA")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, _ := recorded.body["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	parts, _ := first["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("sent %d content parts, want text and image", len(parts))
	}
	image, _ := parts[1].(map[string]any)
	if image["type"] != "image_url" {
		t.Fatalf("second part = %#v, want an image", image)
	}
}

func TestStreamRelaysDeltasInOrder(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, sseResponse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"You "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"spent "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"25 euro."}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	))
	client := newClient(t, url)
	chunks, err := client.Stream(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("what did I spend?")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text strings.Builder
	var final domain.CompletionChunk
	deltas := 0
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.ContentDelta != "" {
			deltas++
			text.WriteString(chunk.ContentDelta)
		}
		if chunk.Done {
			final = chunk
		}
	}
	// Deltas must arrive separately; collapsing them into one defeats the point.
	if deltas != 3 {
		t.Fatalf("received %d deltas, want 3 separate ones", deltas)
	}
	if text.String() != "You spent 25 euro." {
		t.Fatalf("assembled %q", text.String())
	}
	if !final.Done || final.FinishReason != "stop" {
		t.Fatalf("unexpected final chunk %#v", final)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 15 {
		t.Fatalf("usage not reported on the final chunk: %#v", final.Usage)
	}
}

func TestStreamAccumulatesToolCallsAcrossChunks(t *testing.T) {
	t.Parallel()
	// Tool arguments arrive split across chunks and are meaningless until
	// complete, so the adapter must emit them once, whole, at the end.
	url := newServer(t, nil, sseResponse(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query_expenses","arguments":"{\"cat"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"egory\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Food\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	))
	client := newClient(t, url)
	chunks, err := client.Stream(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("food?")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var final domain.CompletionChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if len(chunk.ToolCalls) > 0 && !chunk.Done {
			t.Fatalf("partial tool calls emitted before completion: %#v", chunk.ToolCalls)
		}
		if chunk.Done {
			final = chunk
		}
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(final.ToolCalls))
	}
	call := final.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "query_expenses" || call.ArgsRaw != `{"category":"Food"}` {
		t.Fatalf("tool call not reassembled: %#v", call)
	}
}

func TestStreamAccumulatesParallelToolCallsByIndex(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, sseResponse(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"query_expenses","arguments":"{\"a\":1}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"query_budgets","arguments":"{\"b\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	))
	client := newClient(t, url)
	chunks, err := client.Stream(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("both")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var final domain.CompletionChunk
	for chunk := range chunks {
		if chunk.Done {
			final = chunk
		}
	}
	if len(final.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2 interleaved calls kept apart", len(final.ToolCalls))
	}
	if final.ToolCalls[0].Name != "query_expenses" || final.ToolCalls[0].ArgsRaw != `{"a":1}` {
		t.Fatalf("first call wrong: %#v", final.ToolCalls[0])
	}
	if final.ToolCalls[1].Name != "query_budgets" || final.ToolCalls[1].ArgsRaw != `{"b":2}` {
		t.Fatalf("second call wrong: %#v", final.ToolCalls[1])
	}
}

func TestStreamReportsProviderFailure(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream exploded"}}`)
	})
	client := newClient(t, url)
	chunks, err := client.Stream(context.Background(), domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream returned an immediate error, expected it on the channel: %v", err)
	}
	sawError := false
	for chunk := range chunks {
		if chunk.Err != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected a failing stream to deliver an error chunk")
	}
}

func TestStreamStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()
	url := newServer(t, nil, sseResponse(
		`{"choices":[{"index":0,"delta":{"content":"one"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"two"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	))
	client := newClient(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := client.Stream(ctx, domain.CompletionRequest{Messages: []domain.Message{domain.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Abandoning a stream must not wedge the sending goroutine; cancelling the
	// context is the documented way for a caller to walk away.
	<-chunks
	cancel()
	for range chunks {
	}
}
