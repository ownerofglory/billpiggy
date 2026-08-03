package domain

import "time"

// ObjectReferenceState is the lifecycle of a stored object's reference.
type ObjectReferenceState string

const (
	// ObjectReferenceActive marks an object a live resource still points at.
	ObjectReferenceActive ObjectReferenceState = "active"
	// ObjectReferenceOrphaned marks an object nothing points at any more. A
	// sweeper deletes it from object storage and then forgets the reference.
	ObjectReferenceOrphaned ObjectReferenceState = "orphaned"
)

// ObjectReference records that one stored object belongs to one resource, so a
// replaced or deleted upload can be reclaimed rather than accumulating.
type ObjectReference struct {
	// ObjectKey is the object's location in the store.
	ObjectKey string
	// OwnerID is the user the object belongs to.
	OwnerID string
	// ResourceType names what references the object, such as "expense".
	ResourceType string
	// ResourceID identifies the referencing resource.
	ResourceID string
	// State is the reference's lifecycle position.
	State ObjectReferenceState
	// UpdatedAt is when the reference last changed.
	UpdatedAt time.Time
}
