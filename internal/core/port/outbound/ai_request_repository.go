package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AIRequestRepository records AI provider calls for cost tracking and audit.
type AIRequestRepository interface {
	// RecordRequest appends one AI request record.
	RecordRequest(ctx context.Context, record domain.AIRequestRecord) error
	// Summarize aggregates requests by workload since the given time.
	Summarize(ctx context.Context, since time.Time) (domain.AIUsageSummary, error)
}
