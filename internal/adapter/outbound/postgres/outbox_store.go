package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/pkg/outbox"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// OutboxStore is the PostgreSQL delivery queue behind the outbox engine.
//
// It owns its transactions rather than joining an ambient unit of work: it is
// the component that opens the transaction a projection handler runs inside.
type OutboxStore struct{ pool *pgxpool.Pool }

// NewOutboxStore creates an outbox store over an existing connection pool.
func NewOutboxStore(pool *pgxpool.Pool) *OutboxStore { return &OutboxStore{pool: pool} }

// EnsureSubscription registers a durable subscription and, when it has never
// run before, enqueues every existing event for it.
//
// This is how a newly added projection backfills: history drains through the
// same engine, handler and transaction as live traffic, with Message.Replay set
// so handlers suppress user-visible side effects. There is deliberately no
// separate replay code path to keep correct.
func (s *OutboxStore) EnsureSubscription(ctx context.Context, name string) error {
	if s.pool == nil {
		return errors.New("postgres pool is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin subscription registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted bool
	if err := tx.QueryRow(ctx, `
		insert into events.subscriptions (name) values ($1)
		on conflict (name) do update set name = excluded.name
		returning (xmax = 0) as inserted`, name).Scan(&inserted); err != nil {
		return fmt.Errorf("register subscription %s: %w", name, err)
	}
	if inserted {
		if _, err := tx.Exec(ctx, `
			insert into events.outbox (event_id, subscription, global_seq, aggregate_type, aggregate_id, replay)
			select e.id, $1, e.global_seq, e.aggregate_type, e.aggregate_id, true
			  from events.events e
			 order by e.global_seq`, name); err != nil {
			return fmt.Errorf("backfill subscription %s: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit subscription registration: %w", err)
	}
	return nil
}

// claim locks the next deliverable message for a subscription.
//
// Ordering rests on two things. events.outbox.global_seq gives a real monotonic
// order, unlike the random UUID the previous projector sorted by. The NOT
// EXISTS guard then blocks a message while any earlier message for the same
// aggregate is still outstanding, which is the property the reversal logic in
// the analytics and budget projections actually depends on — global order
// across unrelated aggregates is irrelevant.
//
// The guard is correct because of SKIP LOCKED: a row another worker holds is
// still 'pending' to us, so its successors stay blocked. Dead-lettered rows
// keep blocking too, so a poison event freezes one aggregate's projection
// rather than corrupting it, while every other aggregate keeps flowing.
const claimStatement = `
with claimable as (
    select o.id
      from events.outbox o
     where o.subscription = $1
       and o.status = 'pending'
       and o.available_at <= now()
       and (o.locked_at is null or o.locked_at < now() - make_interval(secs => $2))
       and (cardinality($3::text[]) = 0 or o.aggregate_type = any($3::text[]))
       and not exists (
             select 1
               from events.outbox blocked
              where blocked.subscription   = o.subscription
                and blocked.aggregate_type = o.aggregate_type
                and blocked.aggregate_id   = o.aggregate_id
                and blocked.global_seq     < o.global_seq
                and blocked.status in ('pending', 'dead')
           )
     order by o.global_seq
       for update skip locked
     limit 1
)
update events.outbox o
   set locked_at = now(), locked_by = $4, attempts = o.attempts + 1
  from claimable
 where o.id = claimable.id
returning o.id::text, o.event_id::text, o.global_seq, o.aggregate_type,
          o.aggregate_id::text, o.attempts, o.replay`

// ProcessNext claims the next deliverable message and runs apply inside the
// transaction that also marks it processed.
//
// The claim commits before the handler runs so the lease and the attempt count
// survive a crash: a worker that dies mid-handler leaves a leased row whose
// attempt was already counted, and the lease expiry hands it to someone else
// instead of letting a poison message loop forever.
func (s *OutboxStore) ProcessNext(ctx context.Context, subscription string, aggregateTypes []string, policy outbox.Policy, workerID string, apply func(context.Context, outbox.Message) error) (outbox.Result, error) {
	if s.pool == nil {
		return outbox.Result{}, errors.New("postgres pool is required")
	}
	message, claimed, err := s.claim(ctx, subscription, aggregateTypes, policy, workerID)
	if err != nil || !claimed {
		return outbox.Result{Status: outbox.Idle}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return outbox.Result{}, fmt.Errorf("begin projection transaction: %w", err)
	}
	handlerErr := apply(pgxtx.WithTx(ctx, tx), message)
	if handlerErr == nil {
		handlerErr = s.complete(ctx, tx, subscription, message)
	}
	if handlerErr == nil {
		if err := tx.Commit(ctx); err != nil {
			handlerErr = fmt.Errorf("commit projection: %w", err)
		} else {
			return outbox.Result{Status: outbox.Processed, Message: message}, nil
		}
	}
	_ = tx.Rollback(ctx)
	return s.fail(ctx, message, policy, handlerErr)
}

func (s *OutboxStore) claim(ctx context.Context, subscription string, aggregateTypes []string, policy outbox.Policy, workerID string) (outbox.Message, bool, error) {
	if aggregateTypes == nil {
		aggregateTypes = []string{}
	}
	lease := policy.LeaseTTL
	if lease <= 0 {
		lease = time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return outbox.Message{}, false, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var message outbox.Message
	// Durations cross into SQL as seconds through make_interval rather than as
	// Go's duration text: PostgreSQL intervals have no nanosecond unit and
	// reject spellings such as "1ns" outright.
	err = tx.QueryRow(ctx, claimStatement, subscription, lease.Seconds(), aggregateTypes, workerID).
		Scan(&message.OutboxID, &message.EventID, &message.GlobalSeq, &message.AggregateType,
			&message.AggregateID, &message.Attempts, &message.Replay)
	if errors.Is(err, pgx.ErrNoRows) {
		return outbox.Message{}, false, nil
	}
	if err != nil {
		return outbox.Message{}, false, fmt.Errorf("claim outbox message: %w", err)
	}
	if err := tx.QueryRow(ctx, `select event_type, payload, metadata, occurred_at, aggregate_version from events.events where id = $1`, message.EventID).
		Scan(&message.EventType, &message.Payload, &message.Metadata, &message.OccurredAt, &message.AggregateVersion); err != nil {
		return outbox.Message{}, false, fmt.Errorf("load event %s: %w", message.EventID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return outbox.Message{}, false, fmt.Errorf("commit claim: %w", err)
	}
	return message, true, nil
}

// complete marks the message processed and advances the checkpoint inside the
// handler's own transaction, so the read-model write and the record of it
// having happened commit together.
func (s *OutboxStore) complete(ctx context.Context, tx pgx.Tx, subscription string, message outbox.Message) error {
	if _, err := tx.Exec(ctx, `
		update events.outbox
		   set status = 'processed', processed_at = now(),
		       locked_at = null, locked_by = null, last_error = null
		 where id = $1`, message.OutboxID); err != nil {
		return fmt.Errorf("mark outbox message processed: %w", err)
	}
	// last_global_seq is informational: it feeds operator visibility only.
	// Sequence values are assigned before commit, so gaps are transiently
	// visible and this must never become a "skip everything below" watermark.
	// events.outbox.status is the only authority on what is still undone.
	if _, err := tx.Exec(ctx, `
		insert into events.projector_checkpoints (subscription, last_global_seq, last_event_id, processed_count, updated_at)
		values ($1, $2, $3, 1, now())
		on conflict (subscription) do update
		   set last_global_seq = greatest(events.projector_checkpoints.last_global_seq, excluded.last_global_seq),
		       last_event_id   = excluded.last_event_id,
		       processed_count = events.projector_checkpoints.processed_count + 1,
		       updated_at      = now()`, subscription, message.GlobalSeq, message.EventID); err != nil {
		return fmt.Errorf("advance checkpoint: %w", err)
	}
	return nil
}

// fail records a delivery failure in a fresh transaction, since the handler's
// transaction has already been rolled back.
func (s *OutboxStore) fail(ctx context.Context, message outbox.Message, policy outbox.Policy, cause error) (outbox.Result, error) {
	dead := message.Attempts >= policy.MaxAttempts
	status := outbox.Retried
	if dead {
		status = outbox.DeadLettered
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	if _, err := s.pool.Exec(ctx, `
		update events.outbox
		   set status           = case when $2 then 'dead' else 'pending' end,
		       available_at     = now() + make_interval(secs => $3),
		       dead_lettered_at = case when $2 then now() else null end,
		       locked_at        = null,
		       locked_by        = null,
		       last_error       = left($4, 2000)
		 where id = $1`, message.OutboxID, dead, policy.RetryDelay(message.Attempts).Seconds(), reason); err != nil {
		return outbox.Result{}, fmt.Errorf("record outbox failure: %w", err)
	}
	return outbox.Result{Status: status, Message: message, Err: cause}, nil
}

// Lag reports how many messages are still pending for a subscription.
func (s *OutboxStore) Lag(ctx context.Context, subscription string) (int64, error) {
	if s.pool == nil {
		return 0, errors.New("postgres pool is required")
	}
	var pending int64
	if err := s.pool.QueryRow(ctx, `select count(*) from events.outbox where subscription = $1 and status = 'pending'`, subscription).Scan(&pending); err != nil {
		return 0, fmt.Errorf("read outbox lag: %w", err)
	}
	return pending, nil
}

// DeadLetters returns messages abandoned for a subscription, newest first, so
// operators can inspect why a projection stalled.
func (s *OutboxStore) DeadLetters(ctx context.Context, subscription string, limit int) ([]outbox.Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := pgxtx.From(ctx, s.pool).Query(ctx, `
		select o.id::text, o.event_id::text, o.global_seq, o.aggregate_type, o.aggregate_id::text,
		       o.attempts, o.replay, e.event_type, e.payload, e.metadata, e.occurred_at, e.aggregate_version
		  from events.outbox o join events.events e on e.id = o.event_id
		 where o.subscription = $1 and o.status = 'dead'
		 order by o.dead_lettered_at desc limit $2`, subscription, limit)
	if err != nil {
		return nil, fmt.Errorf("list dead letters: %w", err)
	}
	defer rows.Close()
	messages := make([]outbox.Message, 0)
	for rows.Next() {
		var message outbox.Message
		if err := rows.Scan(&message.OutboxID, &message.EventID, &message.GlobalSeq, &message.AggregateType,
			&message.AggregateID, &message.Attempts, &message.Replay, &message.EventType, &message.Payload,
			&message.Metadata, &message.OccurredAt, &message.AggregateVersion); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
