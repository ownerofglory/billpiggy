package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
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
	// Nothing may be recorded until the stream is fully drained.
	for range chunks {
		if len(requests.Records()) != 0 {
			t.Fatal("request recorded before the stream finished")
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
