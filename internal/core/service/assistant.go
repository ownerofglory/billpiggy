package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// assistantInstructions frame every assistant conversation.
const assistantInstructions = "You are BillPiggy's personal finance assistant. " +
	"Use the query_expenses and query_budgets tools to look up the authenticated " +
	"user's own data before answering; never guess at amounts. " +
	"If a tool returns no matching data, say so. " +
	"Do not provide financial advice as certainty. " +
	"Amounts are given in minor currency units, so 2500 EUR means 25.00 EUR."

const (
	toolQueryExpenses = "query_expenses"
	toolQueryBudgets  = "query_budgets"

	// maxToolRounds bounds how many times the assistant may call a tool while
	// answering one question, so a model that never stops calling tools fails
	// the request instead of looping forever.
	maxToolRounds = 4
	// expenseFetchLimit is how many of the owner's expenses the query_expenses
	// tool considers before applying its own filters and limit.
	expenseFetchLimit = 100
	// defaultToolExpenseResults and maxToolExpenseResults bound what one
	// query_expenses call returns, independent of expenseFetchLimit.
	defaultToolExpenseResults = 20
	maxToolExpenseResults     = 50
)

// AssistantService scopes assistant context to the authenticated user's data.
//
// The model receives no data up front. Instead it is given tools to query the
// owner's own expenses and budgets, and the service dispatches those calls
// against owner-scoped repositories before continuing the conversation. This
// keeps token cost proportional to what the question actually needs, rather
// than pre-loading up to 100 expenses on every request regardless of relevance.
type AssistantService struct {
	provider outbound.AIProvider
	expenses outbound.ExpenseRepository
	budgets  outbound.BudgetRepository
	limit    ratelimit.Limiter
	model    string
}

// NewAssistantService creates a rate-limited assistant service. The default
// limiter is in-memory and process-local; call WithLimiter with a durable
// implementation for multi-replica deployments.
func NewAssistantService(provider outbound.AIProvider, expenses outbound.ExpenseRepository, budgets outbound.BudgetRepository) (*AssistantService, error) {
	if provider == nil || expenses == nil || budgets == nil {
		return nil, fmt.Errorf("assistant provider, expenses, and budgets are required")
	}
	return &AssistantService{
		provider: provider,
		expenses: expenses,
		budgets:  budgets,
		limit:    ratelimit.NewFixedWindow(10, time.Minute),
	}, nil
}

// WithModel overrides the model used for assistant conversations.
func (s *AssistantService) WithModel(model string) *AssistantService {
	s.model = model
	return s
}

// WithLimiter overrides the default in-memory rate limiter, for a durable,
// cross-replica implementation in production.
func (s *AssistantService) WithLimiter(limiter ratelimit.Limiter) *AssistantService {
	s.limit = limiter
	return s
}

// Ask answers one question, dispatching any tool calls the model makes against
// the owner's own data, and returns the complete answer.
func (s *AssistantService) Ask(ctx context.Context, ownerID, message string) (string, error) {
	request, err := s.request(ctx, ownerID, message)
	if err != nil {
		return "", err
	}
	for round := 0; ; round++ {
		completion, err := s.provider.Complete(ctx, request)
		if err != nil {
			return "", err
		}
		if len(completion.ToolCalls) == 0 {
			if strings.TrimSpace(completion.Content) == "" {
				return "", errors.New("assistant returned no answer")
			}
			return completion.Content, nil
		}
		if round >= maxToolRounds {
			return "", fmt.Errorf("assistant did not stop calling tools after %d rounds", maxToolRounds)
		}
		request.Messages = s.resolveToolCalls(ctx, ownerID, request.Messages, completion.ToolCalls)
	}
}

// AskStream answers one question as a stream of incremental updates, so the
// caller can relay tokens to the user as the model produces them. Tool calls
// are resolved internally: only content from the round that produces the
// final answer, and that round's own deltas, reach the caller as they stream.
//
// Callers must drain the returned channel or cancel ctx.
func (s *AssistantService) AskStream(ctx context.Context, ownerID, message string) (<-chan domain.CompletionChunk, error) {
	request, err := s.request(ctx, ownerID, message)
	if err != nil {
		return nil, err
	}
	out := make(chan domain.CompletionChunk)
	go s.streamRounds(ctx, ownerID, request, out)
	return out, nil
}

// streamRounds drives the tool-call loop for AskStream, forwarding content
// deltas as they arrive and only completing the output channel once the model
// answers without requesting another tool.
func (s *AssistantService) streamRounds(ctx context.Context, ownerID string, request domain.CompletionRequest, out chan<- domain.CompletionChunk) {
	defer close(out)
	send := func(chunk domain.CompletionChunk) bool {
		select {
		case out <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for round := 0; ; round++ {
		chunks, err := s.provider.Stream(ctx, request)
		if err != nil {
			send(domain.CompletionChunk{Err: err})
			return
		}
		final, ok := relayRound(chunks, send)
		if !ok {
			return
		}
		if len(final.ToolCalls) == 0 {
			send(final)
			return
		}
		if round >= maxToolRounds {
			send(domain.CompletionChunk{Err: fmt.Errorf("assistant did not stop calling tools after %d rounds", maxToolRounds)})
			return
		}
		request.Messages = s.resolveToolCalls(ctx, ownerID, request.Messages, final.ToolCalls)
	}
}

// relayRound forwards one streamed round's content deltas to send and returns
// its terminal chunk. ok is false when the round failed or ctx was cancelled,
// in which case the caller must stop; relayRound has already reported the
// failure and drained the channel.
func relayRound(chunks <-chan domain.CompletionChunk, send func(domain.CompletionChunk) bool) (domain.CompletionChunk, bool) {
	for chunk := range chunks {
		if chunk.Err != nil {
			send(chunk)
			drain(chunks)
			return domain.CompletionChunk{}, false
		}
		if chunk.Done {
			return chunk, true
		}
		if !send(chunk) {
			drain(chunks)
			return domain.CompletionChunk{}, false
		}
	}
	// The provider closed the channel without a terminal chunk.
	return domain.CompletionChunk{}, false
}

// drain empties a channel so an abandoned provider goroutine does not block on
// a send nobody will read.
func drain(chunks <-chan domain.CompletionChunk) {
	for range chunks {
	}
}

// request validates the question, enforces the per-user rate limit, and builds
// the tool-enabled conversation. The rate limit covers the whole question, not
// each tool round it takes to answer it.
func (s *AssistantService) request(ctx context.Context, ownerID, message string) (domain.CompletionRequest, error) {
	if strings.TrimSpace(message) == "" {
		return domain.CompletionRequest{}, fmt.Errorf("message is required")
	}
	if ownerID == "" {
		return domain.CompletionRequest{}, ErrForbidden
	}
	// Prefixed so this limiter's key namespace never collides with another
	// AI workload's limiter sharing the same durable store.
	allowed, err := s.limit.Allow(ctx, "assistant:"+ownerID)
	if err != nil {
		return domain.CompletionRequest{}, err
	}
	if !allowed {
		return domain.CompletionRequest{}, ErrForbidden
	}
	return domain.CompletionRequest{
		UserID: ownerID,
		Model:  s.model,
		Messages: []domain.Message{
			domain.SystemMessage(assistantInstructions),
			domain.UserMessage(message),
		},
		Tools: assistantTools(),
	}, nil
}

// resolveToolCalls dispatches every requested tool against the owner's own
// data and appends the round to the conversation: every tool-call message
// first, then every result, so they stay grouped for the transport layer to
// merge into one assistant turn followed by its tool results.
func (s *AssistantService) resolveToolCalls(ctx context.Context, ownerID string, messages []domain.Message, calls []domain.ToolCall) []domain.Message {
	for _, call := range calls {
		messages = append(messages, domain.AssistantToolCallMessage(call))
	}
	for _, call := range calls {
		messages = append(messages, domain.ToolResultMessage(call.ID, s.callTool(ctx, ownerID, call)))
	}
	return messages
}

// callTool dispatches one tool call and returns its JSON result, or a JSON
// error object the model can react to.
func (s *AssistantService) callTool(ctx context.Context, ownerID string, call domain.ToolCall) string {
	switch call.Name {
	case toolQueryExpenses:
		return s.queryExpenses(ctx, ownerID, call.ArgsRaw)
	case toolQueryBudgets:
		return s.queryBudgets(ctx, ownerID)
	default:
		return toolErrorResult(fmt.Errorf("unknown tool %q", call.Name))
	}
}

// expenseQueryArgs is the argument shape for the query_expenses tool.
type expenseQueryArgs struct {
	Query string `json:"query"`
	From  string `json:"from"`
	To    string `json:"to"`
	Limit int    `json:"limit"`
}

// queryExpenses searches the owner's own expenses. Date filtering happens
// here rather than in the repository: ExpenseListFilter has no date range
// today, and adding one is a bigger change than this tool needs.
func (s *AssistantService) queryExpenses(ctx context.Context, ownerID, argsRaw string) string {
	var args expenseQueryArgs
	if strings.TrimSpace(argsRaw) != "" {
		if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
			return toolErrorResult(fmt.Errorf("invalid arguments: %w", err))
		}
	}
	from, err := parseOptionalTime(args.From)
	if err != nil {
		return toolErrorResult(fmt.Errorf("invalid from date %q: %w", args.From, err))
	}
	to, err := parseOptionalTime(args.To)
	if err != nil {
		return toolErrorResult(fmt.Errorf("invalid to date %q: %w", args.To, err))
	}
	limit := args.Limit
	if limit <= 0 || limit > maxToolExpenseResults {
		limit = defaultToolExpenseResults
	}

	expenses, err := s.expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: ownerID, Query: args.Query, Limit: expenseFetchLimit})
	if err != nil {
		return toolErrorResult(err)
	}
	matched := make([]assistantExpense, 0, len(expenses))
	for _, expense := range expenses {
		if !from.IsZero() && expense.OccurredAt.Before(from) {
			continue
		}
		if !to.IsZero() && expense.OccurredAt.After(to) {
			continue
		}
		matched = append(matched, assistantExpense{
			Title:       expense.Title,
			AmountMinor: expense.AmountMinor,
			Currency:    expense.Currency,
			Category:    expense.CategoryName,
			OccurredAt:  expense.OccurredAt.Format(time.RFC3339),
		})
	}
	matchedCount := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return toolResult(map[string]any{
		"expenses":       matched,
		"matched_count":  matchedCount,
		"returned_count": len(matched),
	})
}

// queryBudgets lists the owner's own budgets.
func (s *AssistantService) queryBudgets(ctx context.Context, ownerID string) string {
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return toolErrorResult(err)
	}
	converted := make([]assistantBudget, 0, len(budgets))
	for _, budget := range budgets {
		converted = append(converted, assistantBudget{
			Name:       budget.Name,
			LimitMinor: budget.AmountLimitMinor,
			Currency:   budget.Currency,
			Period:     string(budget.Period),
		})
	}
	return toolResult(map[string]any{"budgets": converted})
}

// assistantTools describes the functions the assistant may call. Internal
// identifiers such as expense or category IDs are deliberately absent from
// every result shape: they cost tokens and the model cannot act on them.
func assistantTools() []domain.Tool {
	return []domain.Tool{
		{
			Name: toolQueryExpenses,
			Description: "Search the authenticated user's own expenses. Returns matching expenses with " +
				"title, amount, currency, category and date, most recent first. Amounts are in minor currency units.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Optional text to match against the expense title or category name",
					},
					"from": map[string]any{
						"type":        "string",
						"description": "Optional inclusive start date and time, RFC3339, e.g. 2026-03-01T00:00:00Z",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "Optional inclusive end date and time, RFC3339",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum expenses to return, default 20, max 50",
					},
				},
			},
		},
		{
			Name:        toolQueryBudgets,
			Description: "List the authenticated user's own budgets with their name, limit, currency and period.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// parseOptionalTime parses an RFC3339 timestamp, returning the zero time for
// an empty string.
func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

// toolResult encodes a tool's successful result as JSON.
func toolResult(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolErrorResult(err)
	}
	return string(encoded)
}

// toolErrorResult encodes a tool failure as a small JSON object the model can
// read and react to, rather than failing the whole conversation.
func toolErrorResult(err error) string {
	encoded, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		return `{"error":"internal error"}`
	}
	return string(encoded)
}

// assistantExpense is the trimmed expense shape returned by query_expenses.
type assistantExpense struct {
	Title       string `json:"title"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Category    string `json:"category,omitempty"`
	OccurredAt  string `json:"occurred_at"`
}

// assistantBudget is the trimmed budget shape returned by query_budgets.
type assistantBudget struct {
	Name       string `json:"name"`
	LimitMinor int64  `json:"limit_minor"`
	Currency   string `json:"currency"`
	Period     string `json:"period"`
}
