package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AIRequestRepository records AI provider calls for cost tracking and audit.
type AIRequestRepository interface {
	// RecordRequest appends one AI request record.
	RecordRequest(ctx context.Context, record domain.AIRequestRecord) error
}
