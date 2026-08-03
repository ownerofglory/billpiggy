-- Reshape the outbox from a single hard-wired consumer into a fan-out queue
-- with one delivery row per (event, subscription), monotonic ordering, leases,
-- retry backoff and dead-lettering.

-- A monotonic, gap-tolerant insertion order. aggregate_version orders events
-- within one aggregate; global_seq orders them across the whole store, which is
-- what the queue drains by.
CREATE SEQUENCE IF NOT EXISTS events.event_global_seq;

ALTER TABLE events.events
    ADD COLUMN IF NOT EXISTS global_seq BIGINT;

-- Backfill existing rows in occurrence order so historical replay is ordered.
WITH ordered AS (
    SELECT id, row_number() OVER (ORDER BY occurred_at, id) AS seq
      FROM events.events
     WHERE global_seq IS NULL
)
UPDATE events.events e
   SET global_seq = ordered.seq
  FROM ordered
 WHERE e.id = ordered.id;

SELECT setval('events.event_global_seq',
              GREATEST((SELECT coalesce(max(global_seq), 0) FROM events.events), 1));

ALTER TABLE events.events
    ALTER COLUMN global_seq SET DEFAULT nextval('events.event_global_seq'),
    ALTER COLUMN global_seq SET NOT NULL;

ALTER TABLE events.events ADD CONSTRAINT events_events_global_seq_key UNIQUE (global_seq);

-- Registered consumers. The event store fans a new event out to every row here,
-- so adding a subscription is a data change rather than a schema change.
CREATE TABLE events.subscriptions (
    name TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The legacy outbox held at most one row per event with no subscription. The
-- analytics projection it fed is rebuilt from scratch by migration 000009, so
-- the old rows carry no information worth migrating.
DROP INDEX IF EXISTS events.events_outbox_pending_idx;
TRUNCATE TABLE events.outbox;

ALTER TABLE events.outbox
    DROP CONSTRAINT IF EXISTS outbox_event_id_key,
    ADD COLUMN subscription TEXT NOT NULL REFERENCES events.subscriptions(name) ON DELETE CASCADE,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processed', 'dead')),
    ADD COLUMN global_seq BIGINT NOT NULL,
    ADD COLUMN aggregate_type TEXT NOT NULL,
    ADD COLUMN aggregate_id UUID NOT NULL,
    ADD COLUMN replay BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN dead_lettered_at TIMESTAMPTZ,
    ADD CONSTRAINT events_outbox_event_subscription_key UNIQUE (event_id, subscription);

-- Claim path: pending rows for one subscription, in global order.
CREATE INDEX events_outbox_claim_idx
    ON events.outbox (subscription, global_seq)
    WHERE status = 'pending';

-- Blocker guard: "is an earlier undelivered row outstanding for this aggregate?"
CREATE INDEX events_outbox_aggregate_idx
    ON events.outbox (subscription, aggregate_type, aggregate_id, global_seq)
    WHERE status IN ('pending', 'dead');

CREATE INDEX events_outbox_dead_idx
    ON events.outbox (subscription, dead_lettered_at DESC)
    WHERE status = 'dead';

-- Checkpoints become per-subscription and record progress rather than position.
DROP TABLE IF EXISTS events.projector_checkpoints;

CREATE TABLE events.projector_checkpoints (
    subscription TEXT PRIMARY KEY REFERENCES events.subscriptions(name) ON DELETE CASCADE,
    -- Informational only. Sequence values are assigned before commit, so gaps
    -- are transiently visible and this must never be used as a skip watermark;
    -- events.outbox.status is the only authority on what is still undone.
    last_global_seq BIGINT NOT NULL DEFAULT 0,
    last_event_id UUID,
    processed_count BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
