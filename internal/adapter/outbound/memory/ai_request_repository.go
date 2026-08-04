package memory

import (
	"context"
	"sync"

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
