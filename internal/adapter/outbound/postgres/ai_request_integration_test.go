//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

func TestAIRequestRepositorySummarize(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewAIRequestRepository(pool)
	owner := seedUser(t, pool, "ai-usage@example.test")
	ctx := context.Background()
	now := time.Now()

	// ID is left empty so RecordRequest generates a valid UUID; the ai.requests
	// table's id column is UUID, not an arbitrary string.
	if err := repository.RecordRequest(ctx, domain.AIRequestRecord{
		UserID: owner, Workload: domain.AIWorkloadAssistant, Model: "gpt-5.6-luna",
		Usage: domain.TokenUsage{InputTokens: 100, OutputTokens: 40, TotalTokens: 140}, Outcome: domain.AIRequestSuccess, CreatedAt: now,
	}); err != nil {
		t.Fatalf("record request 1: %v", err)
	}
	if err := repository.RecordRequest(ctx, domain.AIRequestRecord{
		UserID: owner, Workload: domain.AIWorkloadAssistant, Model: "gpt-5.6-luna",
		Usage: domain.TokenUsage{InputTokens: 50, OutputTokens: 0}, Outcome: domain.AIRequestError, ErrorMessage: "timeout", CreatedAt: now,
	}); err != nil {
		t.Fatalf("record request 2: %v", err)
	}
	// Outside the summarized window: must not be counted.
	if err := repository.RecordRequest(ctx, domain.AIRequestRecord{
		UserID: owner, Workload: domain.AIWorkloadAssistant, Model: "gpt-5.6-luna",
		Usage: domain.TokenUsage{InputTokens: 999}, Outcome: domain.AIRequestSuccess, CreatedAt: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("record request 3: %v", err)
	}

	summary, err := repository.Summarize(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(summary.ByWorkload) != 1 {
		t.Fatalf("workloads = %#v, want exactly one", summary.ByWorkload)
	}
	usage := summary.ByWorkload[0]
	if usage.Workload != domain.AIWorkloadAssistant || usage.RequestCount != 2 || usage.ErrorCount != 1 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.InputTokens != 150 || usage.OutputTokens != 40 {
		t.Fatalf("tokens = %#v, want input=150 output=40", usage)
	}
}
