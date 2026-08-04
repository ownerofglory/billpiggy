package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// newAssistant wires the assistant service over in-memory adapters and returns
// it with the expense store, so a test can seed owner data.
func newAssistant(t *testing.T, provider *memory.AIProvider) (*service.AssistantService, *memory.ExpenseRepository) {
	t.Helper()
	expenses := memory.NewExpenseRepository()
	budgets := memory.NewBudgetRepository()
	assistant, err := service.NewAssistantService(provider, expenses, budgets)
	if err != nil {
		t.Fatalf("build assistant service: %v", err)
	}
	return assistant, expenses
}

func seedExpense(t *testing.T, repository *memory.ExpenseRepository, ownerID, title string, amountMinor int64) {
	t.Helper()
	seedExpenseAt(t, repository, ownerID, title, amountMinor, time.Now().UTC())
}

func seedExpenseAt(t *testing.T, repository *memory.ExpenseRepository, ownerID, title string, amountMinor int64, occurredAt time.Time) {
	t.Helper()
	now := time.Now().UTC()
	err := repository.CreateExpense(context.Background(), domain.ExpenseRecord{
		ID: title + "-" + ownerID, OwnerID: ownerID, Title: title, AmountMinor: amountMinor,
		Currency: "EUR", OccurredAt: occurredAt, CategoryName: "Food", Status: domain.ExpenseConfirmed,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed expense: %v", err)
	}
}

func TestAskReturnsTheProviderAnswer(t *testing.T) {
	t.Parallel()
	assistant, expenses := newAssistant(t, memory.NewAIProvider("You spent 25 euro."))
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)

	answer, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "You spent 25 euro." {
		t.Fatalf("answer = %q", answer)
	}
}

func TestAskRejectsAnEmptyMessage(t *testing.T) {
	t.Parallel()
	assistant, _ := newAssistant(t, memory.NewAIProvider("unused"))
	if _, err := assistant.Ask(context.Background(), "owner-1", "   "); err == nil {
		t.Fatal("expected an empty message to be rejected")
	}
}

func TestAskDeclaresQueryToolsUpFront(t *testing.T) {
	t.Parallel()
	// No expense or budget data is pushed into the prompt any more; the model
	// is only told what tools it has and must call them to see anything.
	provider := memory.NewAIProvider("ok")
	assistant, _ := newAssistant(t, provider)

	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(requests))
	}
	if len(requests[0].Messages) != 2 {
		t.Fatalf("sent %d messages, want instructions and the question only", len(requests[0].Messages))
	}
	names := map[string]bool{}
	for _, tool := range requests[0].Tools {
		names[tool.Name] = true
	}
	if !names["query_expenses"] || !names["query_budgets"] {
		t.Fatalf("declared tools = %#v, want query_expenses and query_budgets", requests[0].Tools)
	}
}

func TestAskDispatchesQueryExpensesAndReturnsTheFinalAnswer(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_expenses", ArgsRaw: "{}"}},
		"You spent 25 euro on cinema.",
	)
	assistant, expenses := newAssistant(t, provider)
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)

	answer, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "You spent 25 euro on cinema." {
		t.Fatalf("answer = %q", answer)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider saw %d requests, want an initial call and one after the tool result", len(requests))
	}
	second := requests[1].Messages
	// The second round must carry the assistant's tool request and its result,
	// in addition to the original instructions and question.
	if len(second) != 4 {
		t.Fatalf("second round has %d messages, want instructions, question, tool call and tool result: %#v", len(second), second)
	}
	toolCallMessage := second[2]
	if toolCallMessage.Role != domain.RoleAssistant || toolCallMessage.ToolCallID == nil || *toolCallMessage.ToolCallID != "call_1" {
		t.Fatalf("tool-call message = %#v", toolCallMessage)
	}
	toolResultMessage := second[3]
	if toolResultMessage.Role != domain.RoleTool || toolResultMessage.ToolCallID == nil || *toolResultMessage.ToolCallID != "call_1" {
		t.Fatalf("tool-result message = %#v", toolResultMessage)
	}
	var result struct {
		Expenses []struct {
			Title string `json:"title"`
		} `json:"expenses"`
	}
	if err := json.Unmarshal([]byte(*toolResultMessage.Text), &result); err != nil {
		t.Fatalf("tool result is not JSON: %v\n%s", err, *toolResultMessage.Text)
	}
	if len(result.Expenses) != 1 || result.Expenses[0].Title != "Cinema" {
		t.Fatalf("tool result = %#v, want the seeded expense", result)
	}
}

func TestQueryExpensesToolScopesResultsToTheOwner(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_expenses", ArgsRaw: "{}"}}, "ok",
	)
	assistant, expenses := newAssistant(t, provider)
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)
	seedExpense(t, expenses, "owner-2", "SecretYacht", 900_00)

	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	toolResult := *provider.Requests()[1].Messages[3].Text
	if !strings.Contains(toolResult, "Cinema") {
		t.Fatal("the owner's own expense is missing from the tool result")
	}
	if strings.Contains(toolResult, "SecretYacht") {
		t.Fatal("another owner's expense leaked into the tool result")
	}
}

func TestQueryExpensesToolFiltersByDateRange(t *testing.T) {
	t.Parallel()
	inRange := time.Date(2026, time.March, 15, 12, 0, 0, 0, time.UTC)
	outOfRange := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	args, err := json.Marshal(map[string]string{"from": "2026-03-01T00:00:00Z", "to": "2026-03-31T00:00:00Z"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_expenses", ArgsRaw: string(args)}}, "ok",
	)
	expenses := memory.NewExpenseRepository()
	assistant, err := service.NewAssistantService(provider, expenses, memory.NewBudgetRepository())
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	seedExpenseAt(t, expenses, "owner-1", "Cinema", 25_00, inRange)
	seedExpenseAt(t, expenses, "owner-1", "OldConcert", 40_00, outOfRange)

	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend in March?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	toolResult := *provider.Requests()[1].Messages[3].Text
	if !strings.Contains(toolResult, "Cinema") {
		t.Fatal("the in-range expense is missing from the tool result")
	}
	if strings.Contains(toolResult, "OldConcert") {
		t.Fatal("an out-of-range expense leaked past the date filter")
	}
}

func TestQueryExpensesToolRejectsMalformedDates(t *testing.T) {
	t.Parallel()
	args, err := json.Marshal(map[string]string{"from": "not-a-date"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_expenses", ArgsRaw: string(args)}}, "ok",
	)
	assistant, _ := newAssistant(t, provider)
	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	toolResult := *provider.Requests()[1].Messages[3].Text
	if !strings.Contains(toolResult, "error") {
		t.Fatalf("malformed date did not produce a tool error: %s", toolResult)
	}
}

func TestAskStopsAfterTheMaxToolRounds(t *testing.T) {
	t.Parallel()
	// A provider that always requests a tool must not loop forever.
	provider := memory.NewAIProvider("unreachable").WithToolCalls(domain.ToolCall{ID: "call_1", Name: "query_budgets", ArgsRaw: "{}"})
	assistant, _ := newAssistant(t, provider)

	_, err := assistant.Ask(context.Background(), "owner-1", "what are my budgets?")
	if err == nil {
		t.Fatal("expected an error once the tool-round cap is hit")
	}
	// One initial round plus the bounded number of tool rounds, not unbounded.
	if got := len(provider.Requests()); got < 2 || got > 6 {
		t.Fatalf("provider saw %d requests; expected a small bounded number", got)
	}
}

func TestQueryBudgetsToolReturnsTheOwnersBudgets(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_budgets", ArgsRaw: "{}"}}, "You have one budget.",
	)
	budgets := memory.NewBudgetRepository()
	assistant, err := service.NewAssistantService(provider, memory.NewExpenseRepository(), budgets)
	if err != nil {
		t.Fatalf("build assistant: %v", err)
	}
	if err := budgets.CreateBudget(context.Background(), domain.BudgetRecord{
		ID: "budget-1", OwnerID: "owner-1", Name: "Cinema", CategoryID: "category-1",
		AmountLimitMinor: 100_00, Currency: "EUR", ThresholdPercent: 80, Period: domain.BudgetMonthly,
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	answer, err := assistant.Ask(context.Background(), "owner-1", "what are my budgets?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "You have one budget." {
		t.Fatalf("answer = %q", answer)
	}
	toolResult := *provider.Requests()[1].Messages[3].Text
	if !strings.Contains(toolResult, "Cinema") || !strings.Contains(toolResult, "10000") {
		t.Fatalf("tool result missing the seeded budget: %s", toolResult)
	}
}

func TestAskStreamDispatchesToolCallsBeforeStreamingTheAnswer(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("").WithScriptedRounds(
		[]domain.ToolCall{{ID: "call_1", Name: "query_expenses", ArgsRaw: "{}"}},
		"You spent 25 euro on cinema.",
	)
	assistant, expenses := newAssistant(t, provider)
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)

	chunks, err := assistant.AskStream(context.Background(), "owner-1", "what did I spend?")
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	var assembled strings.Builder
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		assembled.WriteString(chunk.ContentDelta)
	}
	if assembled.String() != "You spent 25 euro on cinema." {
		t.Fatalf("assembled %q", assembled.String())
	}
	if len(provider.Requests()) != 2 {
		t.Fatalf("provider saw %d requests, want the tool round and the answer round", len(provider.Requests()))
	}
}

func TestAskStreamDeliversIncrementalDeltas(t *testing.T) {
	t.Parallel()
	assistant, expenses := newAssistant(t, memory.NewAIProvider("You spent 25 euro on cinema."))
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)

	chunks, err := assistant.AskStream(context.Background(), "owner-1", "what did I spend?")
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	var assembled strings.Builder
	deltas, done := 0, false
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.ContentDelta != "" {
			deltas++
			assembled.WriteString(chunk.ContentDelta)
		}
		if chunk.Done {
			done = true
		}
	}
	if deltas < 2 {
		t.Fatalf("received %d deltas; streaming must deliver more than one", deltas)
	}
	if assembled.String() != "You spent 25 euro on cinema." {
		t.Fatalf("assembled %q", assembled.String())
	}
	if !done {
		t.Fatal("stream ended without a final chunk")
	}
}

func TestAskStreamSurfacesProviderFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("provider unavailable")
	assistant, _ := newAssistant(t, memory.NewAIProvider("").WithError(failure))

	chunks, err := assistant.AskStream(context.Background(), "owner-1", "what did I spend?")
	if err != nil {
		t.Fatalf("AskStream: %v", err)
	}
	sawError := false
	for chunk := range chunks {
		if chunk.Err != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected the provider failure to reach the caller as an error chunk")
	}
}

func TestAssistantEnforcesPerUserRateLimit(t *testing.T) {
	t.Parallel()
	assistant, _ := newAssistant(t, memory.NewAIProvider("ok"))
	// The limit is ten requests per minute per user.
	for i := 0; i < 10; i++ {
		if _, err := assistant.Ask(context.Background(), "owner-1", "question"); err != nil {
			t.Fatalf("request %d rejected early: %v", i+1, err)
		}
	}
	if _, err := assistant.Ask(context.Background(), "owner-1", "question"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("11th request returned %v, want ErrForbidden", err)
	}
	// The limit is per user, so a different user is unaffected.
	if _, err := assistant.Ask(context.Background(), "owner-2", "question"); err != nil {
		t.Fatalf("a second user was rate-limited by the first: %v", err)
	}
}

func TestAskStreamIsRateLimitedBeforeCallingTheProvider(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("ok")
	assistant, _ := newAssistant(t, provider)
	for i := 0; i < 10; i++ {
		if _, err := assistant.Ask(context.Background(), "owner-1", "question"); err != nil {
			t.Fatalf("request %d rejected early: %v", i+1, err)
		}
	}
	if _, err := assistant.AskStream(context.Background(), "owner-1", "question"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("stream past the limit returned %v, want ErrForbidden", err)
	}
	if len(provider.Requests()) != 10 {
		t.Fatalf("provider saw %d requests; a rate-limited stream must not reach it", len(provider.Requests()))
	}
}
