package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AuditRepository appends and queries immutable audit records.
type AuditRepository interface {
	// AppendEntry records one entry, ignoring an entry whose source event has
	// already been recorded so replay and redelivery stay idempotent.
	AppendEntry(ctx context.Context, entry domain.AuditEntry) error
	// ListEntries returns audit entries matching the filter, newest first.
	ListEntries(ctx context.Context, filter AuditFilter) ([]domain.AuditEntry, error)
}

// AuditFilter narrows an audit query.
type AuditFilter struct {
	// ActorID optionally restricts results to one actor.
	ActorID string
	// ResourceType optionally restricts results to one aggregate type.
	ResourceType string
	// ResourceID optionally restricts results to one aggregate instance.
	ResourceID string
	// Action optionally restricts results to one event type.
	Action string
	// From and To bound the occurrence time when set.
	From time.Time
	To   time.Time
	// Limit and Offset paginate the result.
	Limit  int
	Offset int
}
