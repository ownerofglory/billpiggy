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
	now := time.Now().UTC()
	err := repository.CreateExpense(context.Background(), domain.ExpenseRecord{
		ID: title + "-" + ownerID, OwnerID: ownerID, Title: title, AmountMinor: amountMinor,
		Currency: "EUR", OccurredAt: now, CategoryName: "Food", Status: domain.ExpenseConfirmed,
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

func TestAskScopesContextToTheOwner(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("ok")
	assistant, expenses := newAssistant(t, provider)
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)
	seedExpense(t, expenses, "owner-2", "SecretYacht", 900_00)

	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(requests))
	}
	var prompt strings.Builder
	for _, message := range requests[0].Messages {
		if message.Text != nil {
			prompt.WriteString(*message.Text)
		}
	}
	if !strings.Contains(prompt.String(), "Cinema") {
		t.Fatal("the owner's own expense is missing from the prompt")
	}
	// Another user's data must never reach the model.
	if strings.Contains(prompt.String(), "SecretYacht") {
		t.Fatal("another owner's expense leaked into the prompt")
	}
}

func TestAskSendsOwnerContextAsJSON(t *testing.T) {
	t.Parallel()
	provider := memory.NewAIProvider("ok")
	assistant, expenses := newAssistant(t, provider)
	seedExpense(t, expenses, "owner-1", "Cinema", 25_00)

	if _, err := assistant.Ask(context.Background(), "owner-1", "what did I spend?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	messages := provider.Requests()[0].Messages
	if len(messages) != 3 {
		t.Fatalf("sent %d messages, want instructions, data and question", len(messages))
	}
	data := *messages[1].Text
	payload := strings.TrimPrefix(data, "Owner data:\n")
	var decoded struct {
		Expenses []struct {
			Title       string `json:"title"`
			AmountMinor int64  `json:"amount_minor"`
		} `json:"expenses"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("owner context is not valid JSON: %v\n%s", err, payload)
	}
	if len(decoded.Expenses) != 1 || decoded.Expenses[0].Title != "Cinema" || decoded.Expenses[0].AmountMinor != 25_00 {
		t.Fatalf("unexpected owner context %#v", decoded)
	}
	if messages[2].Role != domain.RoleUser || *messages[2].Text != "what did I spend?" {
		t.Fatalf("question message = %#v", messages[2])
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
