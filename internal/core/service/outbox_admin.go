package service

import (
	"context"
	"errors"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// deadLetterPageSize bounds how many abandoned deliveries one call returns, so
// an operator endpoint can never scan the whole outbox.
const deadLetterPageSize = 50

// maxDeadLetterPageSize caps what a caller may ask for.
const maxDeadLetterPageSize = 200

// OutboxAdminService lets an operator see why a projection stopped and put an
// abandoned delivery back.
//
// A dead-lettered message blocks every later event for its own aggregate, by
// design: applying a newer expense event after an older one was never applied
// would silently corrupt the rollups. That makes the block correct but
// permanent, so recovering from it has to be a deliberate, authorised action
// rather than something the engine does on its own.
type OutboxAdminService struct {
	repository outbound.OutboxAdminRepository
}

// NewOutboxAdminService creates the dead-letter administration service.
func NewOutboxAdminService(repository outbound.OutboxAdminRepository) (*OutboxAdminService, error) {
	if repository == nil {
		return nil, errors.New("outbox admin repository is required")
	}
	return &OutboxAdminService{repository: repository}, nil
}

// ListDeadLetters returns abandoned deliveries newest first, across every
// subscription when subscription is empty. Restricted to super-administrators.
func (s *OutboxAdminService) ListDeadLetters(ctx context.Context, actor domain.AppUser, subscription string, limit int) ([]domain.DeadLetter, error) {
	if !actor.Role.Allows(domain.PermissionAuditRead) {
		return nil, ErrForbidden
	}
	if limit <= 0 {
		limit = deadLetterPageSize
	}
	if limit > maxDeadLetterPageSize {
		limit = maxDeadLetterPageSize
	}
	return s.repository.ListDeadLetters(ctx, subscription, limit)
}

// RequeueDeadLetter returns one abandoned delivery to the queue, which also
// releases every later message for the same aggregate that was waiting behind
// it. Restricted to super-administrators.
//
// Requeuing a delivery whose cause has not been fixed simply exhausts its
// attempts again and dead-letters it a second time; it cannot corrupt a read
// model, because the handler still runs in the same transaction that records
// the outcome.
func (s *OutboxAdminService) RequeueDeadLetter(ctx context.Context, actor domain.AppUser, outboxID string) error {
	if !actor.Role.Allows(domain.PermissionAuditRead) {
		return ErrForbidden
	}
	if outboxID == "" {
		return errors.New("outbox id is required")
	}
	found, err := s.repository.RequeueDeadLetter(ctx, outboxID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}
