-- Make the audit trail idempotent under replay, and outlive its subjects.

ALTER TABLE audit.entries
    ADD COLUMN IF NOT EXISTS event_id UUID;

-- Lets the projector insert with ON CONFLICT DO NOTHING, so redelivering an
-- event or replaying the whole stream never duplicates an entry. Partial, so
-- entries written directly by services (which have no source event) are exempt.
CREATE UNIQUE INDEX IF NOT EXISTS audit_entries_event_id_key
    ON audit.entries (event_id) WHERE event_id IS NOT NULL;

-- An audit record must survive deletion of the user it refers to; the foreign
-- key would otherwise block the delete or, worse, permanently poison the entry.
ALTER TABLE audit.entries DROP CONSTRAINT IF EXISTS entries_actor_id_fkey;

CREATE INDEX IF NOT EXISTS audit_entries_actor_idx
    ON audit.entries (actor_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_entries_occurred_idx
    ON audit.entries (occurred_at DESC);
