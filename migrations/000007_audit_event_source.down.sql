DROP INDEX IF EXISTS audit.audit_entries_occurred_idx;
DROP INDEX IF EXISTS audit.audit_entries_actor_idx;
DROP INDEX IF EXISTS audit.audit_entries_event_id_key;

ALTER TABLE audit.entries DROP COLUMN IF EXISTS event_id;

ALTER TABLE audit.entries
    ADD CONSTRAINT entries_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES identity.users(id);
