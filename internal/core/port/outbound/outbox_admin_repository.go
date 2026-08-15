package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// OutboxAdminRepository inspects and recovers abandoned outbox deliveries.
//
// This is deliberately separate from the outbox engine's own Store port: the
// engine needs to claim and resolve messages, while operators need to see why
// a delivery was abandoned and put it back. Keeping them apart stops the
// delivery hot path from growing an administrative surface.
type OutboxAdminRepository interface {
	// ListDeadLetters returns abandoned deliveries, newest first. An empty
	// subscription returns them across every subscription.
	ListDeadLetters(ctx context.Context, subscription string, limit int) ([]domain.DeadLetter, error)
	// RequeueDeadLetter returns an abandoned delivery to the queue with its
	// attempt count reset, reporting whether a dead delivery with that id
	// existed. Requeuing also unblocks every later message for the same
	// aggregate, which the delivery ordering guard held back while it stayed
	// dead.
	RequeueDeadLetter(ctx context.Context, outboxID string) (bool, error)
}
