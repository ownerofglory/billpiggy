package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ObjectReferenceRepository records which stored objects are still referenced.
//
// It exists so replaced and deleted uploads can be reclaimed. Object storage
// cannot participate in a database transaction, so marking a key orphaned and
// actually deleting it are separate steps.
type ObjectReferenceRepository interface {
	// TrackObject records an object as referenced by a resource.
	TrackObject(ctx context.Context, reference domain.ObjectReference) error
	// OrphanObjectsFor marks every object referenced by a resource as orphaned,
	// except the key named by keep. Passing an empty keep orphans all of them.
	OrphanObjectsFor(ctx context.Context, resourceType, resourceID, keep string) error
	// ClaimOrphans returns up to limit orphaned references for deletion.
	ClaimOrphans(ctx context.Context, limit int) ([]domain.ObjectReference, error)
	// ForgetObject removes a reference once its object has been deleted.
	ForgetObject(ctx context.Context, objectKey string) error
}
