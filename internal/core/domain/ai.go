package domain

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// MessageRole identifies who produced a message in a model conversation.
type MessageRole string

const (
	// RoleUser is a message from the end user.
	RoleUser MessageRole = "user"
	// RoleSystem carries instructions that frame the whole conversation.
	RoleSystem MessageRole = "system"
	// RoleAssistant is a message the model produced, including tool requests.
	RoleAssistant MessageRole = "assistant"
	// RoleTool carries the result of a tool the model asked to call.
	RoleTool MessageRole = "tool"
)

// Message is one turn in a model conversation.
//
// The optional fields are pointers so an unset field is distinguishable from an
// empty one: a tool result really can be the empty string, and providers treat
// that differently from "no content". Prefer the constructors below over
// populating the fields directly.
type Message struct {
	// Role identifies who produced the message.
	Role MessageRole `json:"role"`
	// Text is the message's textual content.
	Text *string `json:"text,omitempty"`
	// ImageURL references an image for vision requests. A data URI is accepted,
	// which is how BillPiggy passes a normalised receipt without publishing it.
	ImageURL *string `json:"image_url,omitempty"`
	// ToolCallID correlates a tool result with the request that asked for it.
	ToolCallID *string `json:"tool_call_id,omitempty"`
	// ToolName and ToolArgs describe a tool call the assistant requested.
	ToolName *string `json:"tool_name,omitempty"`
	ToolArgs *string `json:"tool_args,omitempty"`
}

// SystemMessage frames a conversation with instructions.
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Text: &text}
}

// UserMessage is a plain textual message from the end user.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Text: &text}
}

// UserImageMessage pairs a prompt with an image for a vision request.
func UserImageMessage(text, imageURL string) Message {
	return Message{Role: RoleUser, Text: &text, ImageURL: &imageURL}
}

// AssistantToolCallMessage records that the assistant requested a tool. It must
// precede the matching tool result in the conversation.
func AssistantToolCallMessage(call ToolCall) Message {
	return Message{Role: RoleAssistant, ToolCallID: &call.ID, ToolName: &call.Name, ToolArgs: &call.ArgsRaw}
}

// ToolResultMessage returns the output of a tool the assistant requested.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{Role: RoleTool, ToolCallID: &toolCallID, Text: &content}
}

// DebugString renders the message for structured logs.
func (m Message) DebugString() string {
	encoded, _ := json.Marshal(m)
	return string(encoded)
}

// Tool is a function the model may ask the application to run.
type Tool struct {
	// Name identifies the tool to the model and to the dispatcher.
	Name string
	// Description tells the model when the tool is appropriate.
	Description string
	// Parameters is the JSON Schema of the tool's arguments.
	Parameters map[string]any
}

// ToolCall is one request from the model to run a tool.
type ToolCall struct {
	// ID correlates the call with its result.
	ID string
	// Name is the requested tool.
	Name string
	// ArgsRaw is the argument JSON exactly as the model produced it. It is kept
	// raw because a model can emit malformed JSON, and the caller decides how
	// to handle that rather than the transport failing the whole completion.
	ArgsRaw string
}

// TokenUsage reports what a completion consumed, for cost metrics and auditing.
type TokenUsage struct {
	// InputTokens, OutputTokens and TotalTokens count the request and response.
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

// Completion is a finished model response.
type Completion struct {
	// Content is the model's textual answer, empty when it only called tools.
	Content string `json:"content"`
	// ToolCalls are the tools the model asked to run.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Usage reports token consumption when the provider reported it.
	Usage TokenUsage `json:"usage"`
	// FinishReason is the provider's reason for stopping, such as "stop",
	// "tool_calls" or "length".
	FinishReason string `json:"finish_reason,omitempty"`
}

// DebugString renders the completion for structured logs.
func (c Completion) DebugString() string {
	encoded, _ := json.Marshal(c)
	return string(encoded)
}

// CompletionChunk is one incremental update from a streaming completion.
//
// Exactly one chunk has Done set, and it is always the last. A chunk carrying
// Err is terminal too: the stream closes after it.
type CompletionChunk struct {
	// ContentDelta is the text produced since the previous chunk.
	ContentDelta string
	// ToolCalls is delivered once, fully accumulated, when the model has
	// finished requesting tools. Partial tool calls are never emitted, because
	// their arguments arrive split across chunks and are useless until complete.
	ToolCalls []ToolCall
	// Usage is reported on the final chunk when the provider supplies it.
	Usage *TokenUsage
	// FinishReason is set on the final chunk.
	FinishReason string
	// Err reports a stream failure. It is always the last chunk.
	Err error
	// Done marks the final chunk of a successful stream.
	Done bool
}

// ResponseFormat constrains a completion to a JSON schema, so a caller can
// unmarshal the answer instead of parsing prose.
type ResponseFormat struct {
	// Name identifies the schema to the provider.
	Name string
	// Description explains the schema's purpose to the model.
	Description string
	// Schema is the JSON Schema, typically from GenerateSchema.
	Schema any
	// Strict makes the provider guarantee the response matches the schema.
	Strict bool
}

// CompletionRequest is one call to a model.
//
// A single request type keeps the provider port to two methods instead of one
// per combination of tools, schema and streaming.
type CompletionRequest struct {
	// Model overrides the provider's configured default when set.
	Model string
	// Messages is the conversation so far.
	Messages []Message
	// Tools are the functions the model may request.
	Tools []Tool
	// ResponseFormat constrains the answer to a schema when set.
	ResponseFormat *ResponseFormat
	// MaxOutputTokens bounds the response length; zero uses the provider default.
	MaxOutputTokens int64
	// Temperature overrides the provider default when set.
	Temperature *float64
}

// GenerateSchema builds the JSON Schema for T, restricted to the subset that
// structured outputs accept: no external references and no extra properties.
func GenerateSchema[T any]() any {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var value T
	return reflector.Reflect(value)
}
