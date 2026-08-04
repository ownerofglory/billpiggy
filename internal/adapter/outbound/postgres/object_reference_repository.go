package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// ObjectReferenceRepository tracks which stored objects are still referenced,
// so replaced and deleted uploads can be reclaimed by the retention sweeper.
type ObjectReferenceRepository struct{ pool *pgxpool.Pool }

// NewObjectReferenceRepository creates an object-reference adapter.
func NewObjectReferenceRepository(pool *pgxpool.Pool) *ObjectReferenceRepository {
	return &ObjectReferenceRepository{pool: pool}
}

// TrackObject records an object as referenced by a resource.
func (r *ObjectReferenceRepository) TrackObject(ctx context.Context, reference domain.ObjectReference) error {
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		insert into files.object_references (object_key, owner_id, resource_type, resource_id, state, updated_at)
		values ($1, $2, $3, $4, 'active', now())
		on conflict (object_key) do update
		   set owner_id = excluded.owner_id, resource_type = excluded.resource_type,
		       resource_id = excluded.resource_id, state = 'active', updated_at = now()`,
		reference.ObjectKey, reference.OwnerID, reference.ResourceType, reference.ResourceID); err != nil {
		return fmt.Errorf("track object %s: %w", reference.ObjectKey, err)
	}
	return nil
}

// OrphanObjectsFor marks every active object referenced by a resource as
// orphaned, except keep. An empty keep orphans all of them, which is what a
// resource deletion uses: no key in the table is ever the empty string, so
// comparing against it matches every active reference.
func (r *ObjectReferenceRepository) OrphanObjectsFor(ctx context.Context, resourceType, resourceID, keep string) error {
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `
		update files.object_references
		   set state = 'orphaned', updated_at = now()
		 where resource_type = $1 and resource_id = $2 and state = 'active' and object_key <> $3`,
		resourceType, resourceID, keep); err != nil {
		return fmt.Errorf("orphan objects for %s %s: %w", resourceType, resourceID, err)
	}
	return nil
}

// ClaimOrphans returns up to limit orphaned references, oldest first.
//
// The claim runs under FOR UPDATE SKIP LOCKED but does not hold a transaction
// across the sweeper's actual object-storage deletion, so it only narrows —
// rather than eliminates — the window where two sweepers could claim the same
// row. That is an acceptable trade for a single-node deployment: Delete and
// ForgetObject are both idempotent, so a duplicate claim costs a wasted delete
// call, never incorrect state.
func (r *ObjectReferenceRepository) ClaimOrphans(ctx context.Context, limit int) ([]domain.ObjectReference, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := pgxtx.From(ctx, r.pool).Query(ctx, `
		select object_key, owner_id::text, resource_type, resource_id, updated_at
		  from files.object_references
		 where state = 'orphaned'
		 order by updated_at
		   for update skip locked
		 limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim orphaned objects: %w", err)
	}
	defer rows.Close()
	orphans := make([]domain.ObjectReference, 0)
	for rows.Next() {
		var reference domain.ObjectReference
		if err := rows.Scan(&reference.ObjectKey, &reference.OwnerID, &reference.ResourceType, &reference.ResourceID, &reference.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan orphaned object: %w", err)
		}
		reference.State = domain.ObjectReferenceOrphaned
		orphans = append(orphans, reference)
	}
	return orphans, rows.Err()
}

// ForgetObject removes a reference once its object has been deleted.
func (r *ObjectReferenceRepository) ForgetObject(ctx context.Context, objectKey string) error {
	if _, err := pgxtx.From(ctx, r.pool).Exec(ctx, `delete from files.object_references where object_key = $1`, objectKey); err != nil {
		return fmt.Errorf("forget object %s: %w", objectKey, err)
	}
	return nil
}
