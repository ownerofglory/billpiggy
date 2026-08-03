-- Track which stored objects are still referenced, so replaced and deleted
-- uploads can be reclaimed instead of accumulating forever.
--
-- Deleting from object storage cannot join a database transaction, so the split
-- is deliberate: a projection marks a key orphaned inside the transaction that
-- made it orphaned, and a background sweeper deletes the object afterwards and
-- removes the row. A crash between the two leaves an orphaned row, which the
-- next sweep retries; the reverse order could delete an object that a
-- rolled-back transaction still references.

CREATE SCHEMA IF NOT EXISTS files;

CREATE TABLE files.object_references (
    object_key TEXT PRIMARY KEY,
    owner_id UUID NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'orphaned')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX files_object_references_resource_idx
    ON files.object_references (resource_type, resource_id) WHERE state = 'active';

CREATE INDEX files_object_references_orphaned_idx
    ON files.object_references (updated_at) WHERE state = 'orphaned';
