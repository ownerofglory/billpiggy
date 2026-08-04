package outbound

import (
	"context"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// ExpenseRepository owns the expenses projection.
type ExpenseRepository interface {
	CreateExpense(ctx context.Context, expense domain.ExpenseRecord) error
	UpdateExpense(ctx context.Context, expense domain.ExpenseRecord) error
	GetExpense(ctx context.Context, ownerID, expenseID string) (domain.ExpenseRecord, error)
	// GetExpenseVisible returns an expense the viewer owns or that is shared
	// with one of sharedGroupIDs, for the read endpoint. GetExpense stays
	// strictly owner-scoped for write-path checks (updating, deleting,
	// attaching a receipt are never shared-group operations).
	GetExpenseVisible(ctx context.Context, viewerID, expenseID string, sharedGroupIDs []string) (domain.ExpenseRecord, error)
	ListExpenses(ctx context.Context, filter ExpenseListFilter) ([]domain.ExpenseRecord, error)
	DeleteExpense(ctx context.Context, ownerID, expenseID string) error
}

// ExpenseListFilter keeps read-side queries scoped to the viewer's own
// expenses plus, when set, expenses shared with any of SharedGroupIDs.
type ExpenseListFilter struct {
	OwnerID    string
	Query      string
	CategoryID string
	TagIDs     []string
	// SharedGroupIDs additionally includes expenses shared with any of these
	// groups, alongside OwnerID's own expenses. Empty means owner-only.
	SharedGroupIDs []string
	// From and To optionally bound OccurredAt to the half-open window
	// [From, To). Either may be zero to leave that bound open.
	From, To time.Time
	Limit    int
	Offset   int
}

// EventStore appends domain events. A production implementation appends an outbox
// record in the same transaction; projections consume that outbox idempotently.
type EventStore interface {
	Append(ctx context.Context, event DomainEvent) error
}

// DomainEvent is the transport-neutral event envelope.
type DomainEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       any
	OccurredAt    int64
	ActorID       string
}
