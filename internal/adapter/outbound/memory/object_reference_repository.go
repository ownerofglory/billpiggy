package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ObjectReferenceRepository is an in-memory object-reference tracker for local
// development and tests.
type ObjectReferenceRepository struct {
	mu         sync.Mutex
	references map[string]domain.ObjectReference
}

// NewObjectReferenceRepository creates an empty object-reference tracker.
func NewObjectReferenceRepository() *ObjectReferenceRepository {
	return &ObjectReferenceRepository{references: map[string]domain.ObjectReference{}}
}

// TrackObject records an object as referenced by a resource.
func (r *ObjectReferenceRepository) TrackObject(_ context.Context, reference domain.ObjectReference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reference.State = domain.ObjectReferenceActive
	r.references[reference.ObjectKey] = reference
	return nil
}

// OrphanObjectsFor marks every active object referenced by a resource as
// orphaned, except keep. An empty keep orphans all of them, which is what a
// resource deletion uses.
func (r *ObjectReferenceRepository) OrphanObjectsFor(_ context.Context, resourceType, resourceID, keep string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, reference := range r.references {
		if reference.ResourceType != resourceType || reference.ResourceID != resourceID {
			continue
		}
		if reference.State != domain.ObjectReferenceActive || key == keep {
			continue
		}
		reference.State = domain.ObjectReferenceOrphaned
		r.references[key] = reference
	}
	return nil
}

// ClaimOrphans returns up to limit orphaned references, oldest first.
func (r *ObjectReferenceRepository) ClaimOrphans(_ context.Context, limit int) ([]domain.ObjectReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	orphans := make([]domain.ObjectReference, 0)
	for _, reference := range r.references {
		if reference.State == domain.ObjectReferenceOrphaned {
			orphans = append(orphans, reference)
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].UpdatedAt.Before(orphans[j].UpdatedAt) })
	if limit > 0 && len(orphans) > limit {
		orphans = orphans[:limit]
	}
	return orphans, nil
}

// ForgetObject removes a reference once its object has been deleted.
func (r *ObjectReferenceRepository) ForgetObject(_ context.Context, objectKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.references, objectKey)
	return nil
}

// References returns every tracked reference, for test assertions.
func (r *ObjectReferenceRepository) References() []domain.ObjectReference {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make([]domain.ObjectReference, 0, len(r.references))
	for _, reference := range r.references {
		values = append(values, reference)
	}
	return values
}

// Snapshot copies the tracker and returns a function restoring it.
func (r *ObjectReferenceRepository) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := make(map[string]domain.ObjectReference, len(r.references))
	for key, value := range r.references {
		saved[key] = value
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.references = saved
	}
}
