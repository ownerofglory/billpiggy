// Package postgres contains PostgreSQL implementations of outbound ports.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// EventStore appends events and their outbox records.
//
// It joins the caller's transaction when there is one, so a service can commit
// a domain event together with the read-model row it implies. Without an
// ambient transaction each statement commits on its own, which is only safe for
// callers that have nothing else to keep in step.
type EventStore struct{ pool *pgxpool.Pool }

// NewEventStore creates an event-store adapter from an existing connection pool.
func NewEventStore(pool *pgxpool.Pool) *EventStore { return &EventStore{pool: pool} }

// Append writes the event and one outbox row per registered subscription.
//
// The advisory lock serialises version assignment for one aggregate. Because
// the lock is transaction-scoped it is now held for the caller's whole unit of
// work, which is the intended scope: services append before any other write, so
// concurrent commands on the same aggregate take locks in a consistent order.
func (s *EventStore) Append(ctx context.Context, event outbound.DomainEvent) error {
	if s.pool == nil {
		return fmt.Errorf("postgres pool is required")
	}
	eventID, err := uuid.Parse(event.ID)
	if err != nil {
		return fmt.Errorf("parse event id: %w", err)
	}
	aggregateID, err := uuid.Parse(event.AggregateID)
	if err != nil {
		return fmt.Errorf("parse aggregate id: %w", err)
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	metadata, err := json.Marshal(map[string]string{"actor_id": event.ActorID})
	if err != nil {
		return fmt.Errorf("marshal event metadata: %w", err)
	}
	querier := pgxtx.From(ctx, s.pool)
	if _, err := querier.Exec(ctx, "select pg_advisory_xact_lock(hashtext($1))", event.AggregateType+":"+event.AggregateID); err != nil {
		return fmt.Errorf("lock aggregate: %w", err)
	}
	var version int64
	if err := querier.QueryRow(ctx, `select coalesce(max(aggregate_version), 0) + 1 from events.events where aggregate_type = $1 and aggregate_id = $2`, event.AggregateType, aggregateID).Scan(&version); err != nil {
		return fmt.Errorf("read aggregate version: %w", err)
	}
	occurredAt := time.UnixMilli(event.OccurredAt).UTC()
	var globalSeq int64
	if err := querier.QueryRow(ctx, `
		insert into events.events (id, aggregate_type, aggregate_id, aggregate_version, event_type, payload, metadata, occurred_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning global_seq`,
		eventID, event.AggregateType, aggregateID, version, event.EventType, payload, metadata, occurredAt).Scan(&globalSeq); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	// Fan out to every registered subscription. Adding a projection is a row in
	// events.subscriptions rather than a change here.
	if _, err := querier.Exec(ctx, `
		insert into events.outbox (event_id, subscription, global_seq, aggregate_type, aggregate_id)
		select $1, s.name, $2, $3, $4 from events.subscriptions s`,
		eventID, globalSeq, event.AggregateType, aggregateID); err != nil {
		return fmt.Errorf("insert outbox rows: %w", err)
	}
	return nil
}
