-- Deliveries previously had no retry state: a failure was permanent, and a
-- worker crash between claiming a delivery and marking it sent or failed left
-- it stuck in 'processing' forever. attempts/available_at drive exponential
-- retry with a bounded number of attempts before a delivery dead-letters into
-- 'failed'; locked_at/locked_by let a later claim detect and reclaim a lease
-- that expired without the original worker finishing.

ALTER TABLE notifications.deliveries
    ADD COLUMN attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN locked_at TIMESTAMPTZ,
    ADD COLUMN locked_by TEXT;

CREATE INDEX notifications_deliveries_claim_idx
    ON notifications.deliveries (available_at)
    WHERE status IN ('pending', 'processing');

-- An invitation email targets someone who is not a user yet, so it has no
-- identity.users row to reference. recipient_email carries the target
-- address directly for that case; every other notification kind keeps using
-- user_id as before.
ALTER TABLE notifications.deliveries
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN recipient_email TEXT,
    ADD CONSTRAINT deliveries_recipient_check CHECK (user_id IS NOT NULL OR recipient_email IS NOT NULL);
