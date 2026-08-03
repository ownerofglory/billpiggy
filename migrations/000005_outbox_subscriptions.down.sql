DROP TABLE IF EXISTS events.projector_checkpoints;

CREATE TABLE events.projector_checkpoints (
    projector_name TEXT PRIMARY KEY,
    last_event_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DROP INDEX IF EXISTS events.events_outbox_dead_idx;
DROP INDEX IF EXISTS events.events_outbox_aggregate_idx;
DROP INDEX IF EXISTS events.events_outbox_claim_idx;

TRUNCATE TABLE events.outbox;

ALTER TABLE events.outbox
    DROP CONSTRAINT IF EXISTS events_outbox_event_subscription_key,
    DROP COLUMN IF EXISTS dead_lettered_at,
    DROP COLUMN IF EXISTS replay,
    DROP COLUMN IF EXISTS aggregate_id,
    DROP COLUMN IF EXISTS aggregate_type,
    DROP COLUMN IF EXISTS global_seq,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS subscription,
    ADD CONSTRAINT outbox_event_id_key UNIQUE (event_id);

CREATE INDEX events_outbox_pending_idx
    ON events.outbox (available_at, id) WHERE processed_at IS NULL;

DROP TABLE IF EXISTS events.subscriptions;

ALTER TABLE events.events
    DROP CONSTRAINT IF EXISTS events_events_global_seq_key,
    DROP COLUMN IF EXISTS global_seq;

DROP SEQUENCE IF EXISTS events.event_global_seq;
