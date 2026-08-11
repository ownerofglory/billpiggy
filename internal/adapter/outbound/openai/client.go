// Package openai adapts the official OpenAI Go client to BillPiggy's
// provider-neutral AI port.
//
// The domain never sees an OpenAI type: everything crossing the boundary is a
// domain.Message, domain.Tool or domain.Completion, so swapping provider — or
// pointing at a compatible gateway through WithBaseURL — touches this package
// only.
package openai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/packages/ssestream"
	"github.com/openai/openai-go/v2/shared"
	"github.com/openai/openai-go/v2/shared/constant"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// DefaultModel is used when neither the client nor the request names a model.
const DefaultModel = "gpt-5.6-luna"

// defaultMaxOutputTokens bounds a response when the caller does not, so a
// runaway generation cannot quietly become expensive.
const defaultMaxOutputTokens = 800

// Client is the OpenAI-backed implementation of the AI provider port.
type Client struct {
	client *openai.Client
	model  string
	logger *slog.Logger
}

// Option configures a Client.
type Option func(*clientOptions)

type clientOptions struct {
	baseURL string
	model   string
	logger  *slog.Logger
}

// WithBaseURL points the client at an alternative endpoint. It is how tests
// drive the adapter against an httptest server, and how a deployment can route
// through a compatible gateway.
func WithBaseURL(baseURL string) Option {
	return func(o *clientOptions) { o.baseURL = baseURL }
}

// WithModel sets the default model for requests that do not name one.
func WithModel(model string) Option {
	return func(o *clientOptions) { o.model = model }
}

// WithLogger sets the logger used for provider failures.
func WithLogger(logger *slog.Logger) Option {
	return func(o *clientOptions) { o.logger = logger }
}

// NewClient creates an OpenAI client. The API key is required; everything else
// falls back to a sensible default.
func NewClient(apiKey string, options ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	settings := clientOptions{model: DefaultModel, logger: slog.Default()}
	for _, apply := range options {
		apply(&settings)
	}
	if settings.model == "" {
		settings.model = DefaultModel
	}
	if settings.logger == nil {
		settings.logger = slog.Default()
	}
	requestOptions := []option.RequestOption{option.WithAPIKey(apiKey)}
	if settings.baseURL != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(settings.baseURL))
	}
	client := openai.NewClient(requestOptions...)
	return &Client{client: &client, model: settings.model, logger: settings.logger}, nil
}

// Complete returns a finished response, including any tools the model requested.
func (c *Client) Complete(ctx context.Context, request domain.CompletionRequest) (domain.Completion, error) {
	params, err := c.params(request)
	if err != nil {
		return domain.Completion{}, err
	}
	response, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		c.logger.Error("openai completion failed", "model", params.Model, "error", err)
		return domain.Completion{}, fmt.Errorf("openai completion: %w", err)
	}
	if len(response.Choices) == 0 {
		return domain.Completion{}, errors.New("openai returned no choices")
	}
	choice := response.Choices[0]
	return domain.Completion{
		Content:      choice.Message.Content,
		ToolCalls:    convertToolCalls(choice.Message.ToolCalls),
		Usage:        convertUsage(response.Usage),
		FinishReason: choice.FinishReason,
	}, nil
}

// Stream returns incremental updates for a completion.
//
// Text deltas are forwarded as they arrive. Tool calls are not: their arguments
// are split across chunks and are meaningless until complete, so they are
// accumulated and emitted once at the end.
func (c *Client) Stream(ctx context.Context, request domain.CompletionRequest) (<-chan domain.CompletionChunk, error) {
	params, err := c.params(request)
	if err != nil {
		return nil, err
	}
	stream := c.client.Chat.Completions.NewStreaming(ctx, params)
	chunks := make(chan domain.CompletionChunk)
	go c.forward(ctx, stream, chunks)
	return chunks, nil
}

// forward relays a provider stream onto the domain channel. It owns the channel
// and always closes it.
func (c *Client) forward(ctx context.Context, stream *ssestream.Stream[openai.ChatCompletionChunk], chunks chan<- domain.CompletionChunk) {
	defer close(chunks)
	defer func() { _ = stream.Close() }()

	// Tool-call fragments arrive keyed by index, with the id and name on the
	// first fragment and the arguments split across the rest.
	pending := map[int64]*domain.ToolCall{}
	var usage *domain.TokenUsage
	finishReason := ""

	send := func(chunk domain.CompletionChunk) bool {
		select {
		case chunks <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for stream.Next() {
		current := stream.Current()
		if current.Usage.TotalTokens != 0 {
			converted := convertUsage(current.Usage)
			usage = &converted
		}
		for _, choice := range current.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			for _, call := range choice.Delta.ToolCalls {
				accumulate(pending, call)
			}
			if choice.Delta.Content == "" {
				continue
			}
			if !send(domain.CompletionChunk{ContentDelta: choice.Delta.Content}) {
				return
			}
		}
	}
	if err := stream.Err(); err != nil {
		c.logger.Error("openai stream failed", "error", err)
		send(domain.CompletionChunk{Err: fmt.Errorf("openai stream: %w", err)})
		return
	}
	send(domain.CompletionChunk{
		ToolCalls:    collect(pending),
		Usage:        usage,
		FinishReason: finishReason,
		Done:         true,
	})
}

// accumulate merges one streamed tool-call fragment into the call being built.
func accumulate(pending map[int64]*domain.ToolCall, fragment openai.ChatCompletionChunkChoiceDeltaToolCall) {
	call, exists := pending[fragment.Index]
	if !exists {
		call = &domain.ToolCall{}
		pending[fragment.Index] = call
	}
	if fragment.ID != "" {
		call.ID = fragment.ID
	}
	if fragment.Function.Name != "" {
		call.Name = fragment.Function.Name
	}
	call.ArgsRaw += fragment.Function.Arguments
}

// collect returns the accumulated tool calls in the order the model indexed them.
func collect(pending map[int64]*domain.ToolCall) []domain.ToolCall {
	if len(pending) == 0 {
		return nil
	}
	indexes := make([]int64, 0, len(pending))
	for index := range pending {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	calls := make([]domain.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		calls = append(calls, *pending[index])
	}
	return calls
}

// params converts a domain request into provider parameters.
func (c *Client) params(request domain.CompletionRequest) (openai.ChatCompletionNewParams, error) {
	if len(request.Messages) == 0 {
		return openai.ChatCompletionNewParams{}, errors.New("completion request has no messages")
	}
	model := request.Model
	if model == "" {
		model = c.model
	}
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	params := openai.ChatCompletionNewParams{
		Model:               model,
		Messages:            convertMessages(request.Messages),
		MaxCompletionTokens: openai.Int(maxTokens),
	}
	if len(request.Tools) > 0 {
		params.Tools = convertTools(request.Tools)
		// Reasoning models default to a non-zero reasoning_effort server-side
		// even when the client omits it, and the Chat Completions API rejects
		// that combination outright once function tools are attached:
		// "Function tools with reasoning_effort are not supported for
		// <model> in /v1/chat/completions. To use function tools, use
		// /v1/responses or set reasoning_effort to 'none'." Forcing it off
		// here is that documented workaround. Scoped to tool-bearing requests
		// only, since that's exactly the condition the API rejects — the
		// receipt- and dictation-extraction requests never set Tools, so
		// they're unaffected either way. "none" isn't among the SDK's own
		// ReasoningEffort constants (minimal/low/medium/high) yet; the type
		// is a bare string, so it's spelled out directly instead.
		params.ReasoningEffort = shared.ReasoningEffort("none")
	}
	if request.Temperature != nil {
		params.Temperature = openai.Float(*request.Temperature)
	}
	if format := request.ResponseFormat; format != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        format.Name,
					Description: openai.String(format.Description),
					Schema:      format.Schema,
					Strict:      openai.Bool(format.Strict),
				},
			},
		}
	}
	return params, nil
}

// convertMessages maps domain messages onto provider message params.
//
// Consecutive assistant tool-call messages are merged into one provider
// message carrying every tool_calls entry, because the Chat Completions API
// expects the tool calls a single turn requested to arrive together, followed
// immediately by their results — not interleaved assistant/tool/assistant/tool
// turns. domain.Message models one call per message for a simpler domain
// shape, so the merge happens here at the transport boundary instead.
func convertMessages(messages []domain.Message) []openai.ChatCompletionMessageParamUnion {
	converted := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		switch message.Role {
		case domain.RoleSystem:
			if message.Text != nil {
				converted = append(converted, openai.SystemMessage(*message.Text))
			}
		case domain.RoleUser:
			parts := make([]openai.ChatCompletionContentPartUnionParam, 0, 2)
			if message.Text != nil {
				parts = append(parts, openai.TextContentPart(*message.Text))
			}
			if message.ImageURL != nil {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: *message.ImageURL}))
			}
			if len(parts) > 0 {
				converted = append(converted, openai.UserMessage(parts))
			}
		case domain.RoleAssistant:
			if !isToolCallMessage(message) {
				if message.Text != nil {
					converted = append(converted, openai.AssistantMessage(*message.Text))
				}
				continue
			}
			calls := []openai.ChatCompletionMessageToolCallUnionParam{toolCallParam(message)}
			for index+1 < len(messages) && isToolCallMessage(messages[index+1]) {
				index++
				calls = append(calls, toolCallParam(messages[index]))
			}
			converted = append(converted, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{ToolCalls: calls},
			})
		case domain.RoleTool:
			if message.ToolCallID != nil && message.Text != nil {
				converted = append(converted, openai.ToolMessage(*message.Text, *message.ToolCallID))
			}
		}
	}
	return converted
}

// isToolCallMessage reports whether a domain message represents one requested
// tool call rather than plain assistant text.
func isToolCallMessage(message domain.Message) bool {
	return message.Role == domain.RoleAssistant && message.ToolCallID != nil && message.ToolName != nil && message.ToolArgs != nil
}

// toolCallParam converts one tool-call domain message into a provider tool_calls entry.
func toolCallParam(message domain.Message) openai.ChatCompletionMessageToolCallUnionParam {
	return openai.ChatCompletionMessageToolCallUnionParam{
		OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
			ID: *message.ToolCallID,
			Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
				Name:      *message.ToolName,
				Arguments: *message.ToolArgs,
			},
		},
	}
}

// convertTools maps domain tools onto provider tool params.
func convertTools(tools []domain.Tool) []openai.ChatCompletionToolUnionParam {
	converted := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Type: constant.Function("function"),
				Function: shared.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: openai.String(tool.Description),
					Parameters:  shared.FunctionParameters(tool.Parameters),
				},
			},
		})
	}
	return converted
}

// convertToolCalls maps provider tool calls onto domain tool calls, skipping
// variants this application does not use.
func convertToolCalls(calls []openai.ChatCompletionMessageToolCallUnion) []domain.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	converted := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Type != "" && call.Type != "function" {
			continue
		}
		converted = append(converted, domain.ToolCall{ID: call.ID, Name: call.Function.Name, ArgsRaw: call.Function.Arguments})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

// convertUsage maps provider token usage onto the domain equivalent.
func convertUsage(usage openai.CompletionUsage) domain.TokenUsage {
	return domain.TokenUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
}
