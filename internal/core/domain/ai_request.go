package domain

import "time"

// AIWorkload names which AI-backed feature produced a request.
type AIWorkload string

const (
	// AIWorkloadAssistant is a conversational assistant turn.
	AIWorkloadAssistant AIWorkload = "assistant"
	// AIWorkloadReceiptExtraction is a photo/document receipt scan.
	AIWorkloadReceiptExtraction AIWorkload = "receipt_extraction"
	// AIWorkloadSentenceExtraction is free-text "intelligent" expense entry.
	AIWorkloadSentenceExtraction AIWorkload = "sentence_extraction"
	// AIWorkloadTranscription is audio-to-text dictation.
	AIWorkloadTranscription AIWorkload = "transcription"
)

// AIRequestOutcome records whether an AI provider call succeeded.
type AIRequestOutcome string

const (
	// AIRequestSuccess marks a request that returned a usable result.
	AIRequestSuccess AIRequestOutcome = "success"
	// AIRequestError marks a request that failed.
	AIRequestError AIRequestOutcome = "error"
)

// AIRequestRecord is one AI provider call, kept for cost tracking and audit.
//
// It is written directly by the calling service rather than projected from a
// domain event: an AI request is an external side effect with a cost, not a
// state change to an aggregate.
type AIRequestRecord struct {
	// ID identifies the record.
	ID string
	// UserID is the user the request was made on behalf of.
	UserID string
	// Workload names which feature made the request.
	Workload AIWorkload
	// Model is the provider model used.
	Model string
	// Usage reports token consumption when the provider reported it.
	Usage TokenUsage
	// LatencyMS is how long the provider call took.
	LatencyMS int64
	// Outcome reports whether the call succeeded.
	Outcome AIRequestOutcome
	// ErrorMessage holds the failure reason when Outcome is AIRequestError.
	ErrorMessage string
	// CreatedAt is when the request was made.
	CreatedAt time.Time
}

// AIWorkloadUsage summarises requests for one workload since a query's start time.
type AIWorkloadUsage struct {
	// Workload names the feature these totals belong to.
	Workload AIWorkload `json:"workload"`
	// RequestCount and ErrorCount tally outcomes.
	RequestCount int64 `json:"requestCount"`
	ErrorCount   int64 `json:"errorCount"`
	// InputTokens and OutputTokens sum token consumption across requests.
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// AIUsageSummary aggregates AI request records by workload for a time window.
type AIUsageSummary struct {
	// Since is the inclusive start of the summarised window.
	Since time.Time `json:"since"`
	// ByWorkload lists one entry per workload that made at least one request.
	ByWorkload []AIWorkloadUsage `json:"byWorkload"`
}
