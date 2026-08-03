package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// AssistantProvider generates answers from an approved external model provider.
type AssistantProvider interface {
	Respond(context.Context, string, string) (string, error)
}

// AssistantService scopes assistant context to the authenticated user's data.
type AssistantService struct {
	provider AssistantProvider
	expenses outbound.ExpenseRepository
	budgets  outbound.BudgetRepository
	limit    *ratelimit.FixedWindow
}

// NewAssistantService creates a rate-limited assistant service.
func NewAssistantService(provider AssistantProvider, expenses outbound.ExpenseRepository, budgets outbound.BudgetRepository) (*AssistantService, error) {
	if provider == nil || expenses == nil || budgets == nil {
		return nil, fmt.Errorf("assistant provider, expenses, and budgets are required")
	}
	return &AssistantService{provider: provider, expenses: expenses, budgets: budgets, limit: ratelimit.NewFixedWindow(10, time.Minute)}, nil
}

// Ask answers one question using only the current user's expense and budget context.
func (s *AssistantService) Ask(ctx context.Context, ownerID, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("message is required")
	}
	if !s.limit.Allow(ownerID) {
		return "", ErrForbidden
	}
	expenses, err := s.expenses.ListExpenses(ctx, outbound.ExpenseListFilter{OwnerID: ownerID, Limit: 100})
	if err != nil {
		return "", err
	}
	budgets, err := s.budgets.ListBudgets(ctx, ownerID, nil)
	if err != nil {
		return "", err
	}
	return s.provider.Respond(ctx, "You are BillPiggy's personal finance assistant. Answer only from the provided owner-scoped expense and budget data. If the data does not answer the question, say so. Do not provide financial advice as certainty.", fmt.Sprintf("Question: %s\nExpenses: %#v\nBudgets: %#v", message, expenses, budgets))
}
