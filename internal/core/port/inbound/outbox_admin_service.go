package inbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// OutboxAdminService is everything an HTTP handler needs to inspect and
// recover abandoned outbox deliveries.
type OutboxAdminService interface {
	ListDeadLetters(ctx context.Context, actor domain.AppUser, subscription string, limit int) ([]domain.DeadLetter, error)
	RequeueDeadLetter(ctx context.Context, actor domain.AppUser, outboxID string) error
}
