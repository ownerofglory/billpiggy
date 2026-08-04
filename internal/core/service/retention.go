package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// RetentionService reclaims object-storage space left behind by replaced or
// deleted uploads.
//
// Deleting from object storage cannot join a database transaction, so the two
// steps are deliberately split: a resource-owning service marks a key orphaned
// inside the same transaction that stopped referencing it, and this service
// deletes the object afterwards and forgets the reference. A crash between the
// two leaves an orphaned row, which the next sweep simply retries.
type RetentionService struct {
	references outbound.ObjectReferenceRepository
	objects    outbound.ObjectStore
}

// NewRetentionService creates a retention sweeper.
func NewRetentionService(references outbound.ObjectReferenceRepository, objects outbound.ObjectStore) (*RetentionService, error) {
	if references == nil || objects == nil {
		return nil, errors.New("object reference repository and object store are required")
	}
	return &RetentionService{references: references, objects: objects}, nil
}

// SweepOrphans deletes up to limit orphaned objects and forgets their
// references. A deletion failure for one object leaves it orphaned for the
// next sweep instead of aborting the batch.
func (s *RetentionService) SweepOrphans(ctx context.Context, limit int) (int, error) {
	orphans, err := s.references.ClaimOrphans(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("claim orphaned objects: %w", err)
	}
	swept := 0
	for _, orphan := range orphans {
		if err := s.objects.Delete(ctx, orphan.ObjectKey); err != nil {
			continue
		}
		if err := s.references.ForgetObject(ctx, orphan.ObjectKey); err != nil {
			return swept, fmt.Errorf("forget object %s: %w", orphan.ObjectKey, err)
		}
		swept++
	}
	return swept, nil
}
