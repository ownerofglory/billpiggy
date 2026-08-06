package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/metrics"
)

func TestAuditedAIProviderRecordsASuccessfulComplete(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("You spent 25 euro."), requests, domain.AIWorkloadAssistant)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	completion, err := provider.Complete(context.Background(), domain.CompletionRequest{
		UserID: "owner-1", Model: "gpt-5.6-luna", Messages: []domain.Message{domain.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completion.Content != "You spent 25 euro." {
		t.Fatalf("completion content = %q", completion.Content)
	}
	records := requests.Records()
	if len(records) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(records))
	}
	record := records[0]
	if record.UserID != "owner-1" || record.Workload != domain.AIWorkloadAssistant || record.Model != "gpt-5.6-luna" {
		t.Fatalf("unexpected record %#v", record)
	}
	if record.Outcome != domain.AIRequestSuccess {
		t.Fatalf("outcome = %s, want success", record.Outcome)
	}
	if record.Usage.TotalTokens == 0 {
		t.Fatal("expected usage to be recorded from the completion")
	}
}

func TestAuditedAIProviderRecordsAFailedComplete(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	failure := errors.New("provider unavailable")
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("").WithError(failure), requests, domain.AIWorkloadReceiptExtraction)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	if _, err := provider.Complete(context.Background(), domain.CompletionRequest{
		UserID: "owner-1", Model: "gpt-4o-mini", Messages: []domain.Message{domain.UserMessage("hi")},
	}); !errors.Is(err, failure) {
		t.Fatalf("Complete returned %v, want the provider failure", err)
	}
	records := requests.Records()
	if len(records) != 1 || records[0].Outcome != domain.AIRequestError || records[0].ErrorMessage == "" {
		t.Fatalf("unexpected records %#v", records)
	}
	if records[0].Workload != domain.AIWorkloadReceiptExtraction {
		t.Fatalf("workload = %s, want receipt_extraction", records[0].Workload)
	}
}

func TestAuditedAIProviderRecordsAStreamAfterItFinishes(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("You spent 25 euro."), requests, domain.AIWorkloadAssistant)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	chunks, err := provider.Stream(context.Background(), domain.CompletionRequest{
		UserID: "owner-1", Model: "gpt-5.6-luna", Messages: []domain.Message{domain.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Nothing may be recorded while the stream is still producing content.
	//
	// The final chunk is deliberately exempt. Stream hands the last chunk over
	// an unbuffered channel and only then records, so a consumer's final loop
	// body always races that write — asserting on it made this test flaky.
	// The guarantee that actually matters is the one checked after the loop:
	// the record is always in place by the time the channel closes.
	observed := make([]int, 0, 8)
	for range chunks {
		observed = append(observed, len(requests.Records()))
	}
	if len(observed) < 2 {
		t.Fatalf("stream produced %d chunk(s); the mid-stream check needs several to mean anything", len(observed))
	}
	for index, count := range observed[:len(observed)-1] {
		if count != 0 {
			t.Fatalf("request recorded mid-stream, after chunk %d of %d", index+1, len(observed))
		}
	}
	records := requests.Records()
	if len(records) != 1 || records[0].Outcome != domain.AIRequestSuccess {
		t.Fatalf("unexpected records after the stream finished: %#v", records)
	}
}

func TestAuditedAIProviderRecordsAFailedStream(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	failure := errors.New("provider unavailable")
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("").WithError(failure), requests, domain.AIWorkloadTranscription)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	chunks, err := provider.Stream(context.Background(), domain.CompletionRequest{UserID: "owner-1", Messages: []domain.Message{domain.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	sawError := false
	for chunk := range chunks {
		if chunk.Err != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected the failure to reach the caller through a chunk")
	}
	records := requests.Records()
	if len(records) != 1 || records[0].Outcome != domain.AIRequestError {
		t.Fatalf("unexpected records %#v", records)
	}
}

func TestAuditedAIProviderRecordsMetricsWhenWired(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	registry := metrics.NewRegistry()
	calls := registry.NewCounterVec("billpiggy_ai_requests_total", "Total AI provider calls.", "workload", "outcome")
	tokens := registry.NewCounterVec("billpiggy_ai_tokens_total", "Total AI token usage.", "workload", "direction")
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("You spent 25 euro."), requests, domain.AIWorkloadAssistant)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	provider = provider.WithMetrics(calls, tokens)

	if _, err := provider.Complete(context.Background(), domain.CompletionRequest{
		UserID: "owner-1", Model: "gpt-5.6-luna", Messages: []domain.Message{domain.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var buf strings.Builder
	if err := registry.Render(&buf); err != nil {
		t.Fatalf("render metrics: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, `billpiggy_ai_requests_total{workload="assistant",outcome="success"} 1`) {
		t.Fatalf("missing call metric: %s", output)
	}
	if !strings.Contains(output, `billpiggy_ai_tokens_total{workload="assistant",direction="input"}`) {
		t.Fatalf("missing input token metric: %s", output)
	}
	if !strings.Contains(output, `billpiggy_ai_tokens_total{workload="assistant",direction="output"}`) {
		t.Fatalf("missing output token metric: %s", output)
	}
}

func TestAuditedAIProviderWithoutMetricsWiredStillRecordsAudit(t *testing.T) {
	t.Parallel()
	requests := memory.NewAIRequestRepository()
	provider, err := service.NewAuditedAIProvider(memory.NewAIProvider("hi"), requests, domain.AIWorkloadAssistant)
	if err != nil {
		t.Fatalf("build audited provider: %v", err)
	}
	if _, err := provider.Complete(context.Background(), domain.CompletionRequest{UserID: "owner-1", Messages: []domain.Message{domain.UserMessage("hi")}}); err != nil {
		t.Fatalf("Complete without metrics wired should still succeed: %v", err)
	}
	if len(requests.Records()) != 1 {
		t.Fatal("expected audit recording to proceed without metrics wired")
	}
}
