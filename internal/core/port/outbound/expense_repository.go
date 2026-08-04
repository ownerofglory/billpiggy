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
	ListExpenses(ctx context.Context, filter ExpenseListFilter) ([]domain.ExpenseRecord, error)
	DeleteExpense(ctx context.Context, ownerID, expenseID string) error
}

// ExpenseListFilter keeps all read-side queries owner-scoped.
type ExpenseListFilter struct {
	OwnerID    string
	Query      string
	CategoryID string
	TagIDs     []string
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
