-- Deliveries with no user_id (invitation emails) cannot survive restoring the
-- NOT NULL constraint; they are deleted rather than left to fail the migration.
DELETE FROM notifications.deliveries WHERE user_id IS NULL;
ALTER TABLE notifications.deliveries
    DROP CONSTRAINT deliveries_recipient_check,
    DROP COLUMN recipient_email,
    ALTER COLUMN user_id SET NOT NULL;

DROP INDEX IF EXISTS notifications.notifications_deliveries_claim_idx;
ALTER TABLE notifications.deliveries
    DROP COLUMN locked_by,
    DROP COLUMN locked_at,
    DROP COLUMN available_at,
    DROP COLUMN attempts;
