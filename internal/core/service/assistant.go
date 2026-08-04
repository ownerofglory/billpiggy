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
	"Answer only from the provided owner-scoped expense and budget data. " +
	"If the data does not answer the question, say so. " +
	"Do not provide financial advice as certainty. " +
	"Amounts are given in minor currency units, so 2500 EUR means 25.00 EUR."

// assistantContextLimit bounds how many expenses are put in front of the model.
const assistantContextLimit = 100

// AssistantService scopes assistant context to the authenticated user's data.
type AssistantService struct {
	provider outbound.AIProvider
	expenses outbound.ExpenseRepository
	budgets  outbound.BudgetRepository
	limit    *ratelimit.FixedWindow
	model    string
}

// NewAssistantService creates a rate-limited assistant service.
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

// Ask answers one question using only the current user's expense and budget
// context, returning the complete answer.
func (s *AssistantService) Ask(ctx context.Context, ownerID, message string) (string, error) {
	request, err := s.request(ctx, ownerID, message)
	if err != nil {
		return "", err
	}
	completion, err := s.provider.Complete(ctx, request)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(completion.Content) == "" {
		return "", errors.New("assistant returned no answer")
	}
	return completion.Content, nil
}

// AskStream answers one question as a stream of incremental updates, so the
// caller can relay tokens to the user as the model produces them rather than
// waiting for the whole answer.
//
// Callers must drain the returned channel or cancel ctx.
func (s *AssistantService) AskStream(ctx context.Context, ownerID, message string) (<-chan domain.CompletionChunk, error) {
	request, err := s.request(ctx, ownerID, message)
	if err != nil {
		return nil, err
	}
	return s.provider.Stream(ctx, request)
}

// request validates the question, enforces the per-user rate limit, and builds
// the owner-scoped conversation.
func (s *AssistantService) request(ctx context.Context, ownerID, message string) (domain.CompletionRequest, error) {
	if strings.TrimSpace(message) == "" {
		return domain.CompletionRequest{}, fmt.Errorf("message is required")
	}
	if ownerID == "" {
		return domain.CompletionRequest{}, ErrForbidden
	}
	if !s.limit.Allow(ownerID) {
		return domain.CompletionRequest{}, ErrForbidden
	}
	ownerData, err := s.ownerContext(ctx, ownerID)
	if err != nil {
		return domain.CompletionRequest{}, err
	}
	return domain.CompletionRequest{
		Model: s.model,
		Messages: []domain.Message{
			domain.SystemMessage(assistantInstructions),
			domain.SystemMessage(ownerData),
			domain.UserMessage(message),
		},
	}, nil
}

// ownerContext renders the user's own expenses and budgets as JSON.
//
// JSON rather than Go's %#v: the struct syntax the previous implementation used
// spent tokens on type names and pointer notation the model has to work past,
// and it leaked internal field names that mean nothing outside the codebase.
func (s *AssistantService) ownerContext(ctx context.Context, ownerID string) (string, error) {
	expenses, err := s.expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: ownerID, Limit: assistantContextLimit})
	if err != nil {
		return "", err
	}
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return "", err
	}
	payload := struct {
		Expenses []assistantExpense `json:"expenses"`
		Budgets  []assistantBudget  `json:"budgets"`
	}{
		Expenses: make([]assistantExpense, 0, len(expenses)),
		Budgets:  make([]assistantBudget, 0, len(budgets)),
	}
	for _, expense := range expenses {
		payload.Expenses = append(payload.Expenses, assistantExpense{
			Title:       expense.Title,
			AmountMinor: expense.AmountMinor,
			Currency:    expense.Currency,
			Category:    expense.CategoryName,
			OccurredAt:  expense.OccurredAt.Format(time.RFC3339),
		})
	}
	for _, budget := range budgets {
		payload.Budgets = append(payload.Budgets, assistantBudget{
			Name:       budget.Name,
			LimitMinor: budget.AmountLimitMinor,
			Currency:   budget.Currency,
			Period:     string(budget.Period),
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode assistant context: %w", err)
	}
	return "Owner data:\n" + string(encoded), nil
}

// assistantExpense is the trimmed expense shape shown to the model. Internal
// identifiers are omitted: they cost tokens and the model cannot act on them.
type assistantExpense struct {
	Title       string `json:"title"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Category    string `json:"category,omitempty"`
	OccurredAt  string `json:"occurred_at"`
}

// assistantBudget is the trimmed budget shape shown to the model.
type assistantBudget struct {
	Name       string `json:"name"`
	LimitMinor int64  `json:"limit_minor"`
	Currency   string `json:"currency"`
	Period     string `json:"period"`
}
