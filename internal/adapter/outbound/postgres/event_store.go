// Package postgres contains PostgreSQL implementations of outbound ports.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// EventStore appends events and their outbox records in one PostgreSQL transaction.
type EventStore struct{ pool *pgxpool.Pool }

// NewEventStore creates an event-store adapter from an existing connection pool.
func NewEventStore(pool *pgxpool.Pool) *EventStore { return &EventStore{pool: pool} }

// Append commits the event and outbox row together. Aggregate locking assigns a contiguous version.
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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin event transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtext($1))", event.AggregateType+":"+event.AggregateID); err != nil {
		return fmt.Errorf("lock aggregate: %w", err)
	}
	var version int64
	if err := tx.QueryRow(ctx, `select coalesce(max(aggregate_version), 0) + 1 from events.events where aggregate_type = $1 and aggregate_id = $2`, event.AggregateType, aggregateID).Scan(&version); err != nil {
		return fmt.Errorf("read aggregate version: %w", err)
	}
	metadata, _ := json.Marshal(map[string]string{"actor_id": event.ActorID})
	occurredAt := time.UnixMilli(event.OccurredAt).UTC()
	if _, err := tx.Exec(ctx, `insert into events.events (id, aggregate_type, aggregate_id, aggregate_version, event_type, payload, metadata, occurred_at) values ($1, $2, $3, $4, $5, $6, $7, $8)`, eventID, event.AggregateType, aggregateID, version, event.EventType, payload, metadata, occurredAt); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into events.outbox (event_id) values ($1)`, eventID); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit event: %w", err)
	}
	return nil
}
