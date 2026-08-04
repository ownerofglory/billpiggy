package memory

import (
	"context"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AIRequestRepository is an in-memory AI request audit trail for local
// development and tests.
type AIRequestRepository struct {
	mu      sync.Mutex
	records []domain.AIRequestRecord
}

// NewAIRequestRepository creates an empty AI request audit trail.
func NewAIRequestRepository() *AIRequestRepository { return &AIRequestRepository{} }

// RecordRequest appends one AI request record.
func (r *AIRequestRepository) RecordRequest(_ context.Context, record domain.AIRequestRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	return nil
}

// Records returns every recorded request, for test assertions.
func (r *AIRequestRepository) Records() []domain.AIRequestRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.AIRequestRecord(nil), r.records...)
}

// Summarize aggregates requests by workload since the given time.
func (r *AIRequestRepository) Summarize(_ context.Context, since time.Time) (domain.AIUsageSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	totals := map[domain.AIWorkload]*domain.AIWorkloadUsage{}
	order := []domain.AIWorkload{}
	for _, record := range r.records {
		if record.CreatedAt.Before(since) {
			continue
		}
		usage, ok := totals[record.Workload]
		if !ok {
			usage = &domain.AIWorkloadUsage{Workload: record.Workload}
			totals[record.Workload] = usage
			order = append(order, record.Workload)
		}
		usage.RequestCount++
		if record.Outcome == domain.AIRequestError {
			usage.ErrorCount++
		}
		usage.InputTokens += record.Usage.InputTokens
		usage.OutputTokens += record.Usage.OutputTokens
	}
	summary := domain.AIUsageSummary{Since: since}
	for _, workload := range order {
		summary.ByWorkload = append(summary.ByWorkload, *totals[workload])
	}
	return summary, nil
}
