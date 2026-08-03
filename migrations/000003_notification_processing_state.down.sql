UPDATE notifications.deliveries SET status = 'failed', failure_reason = 'migration rollback' WHERE status = 'processing';
ALTER TABLE notifications.deliveries DROP CONSTRAINT deliveries_status_check;
ALTER TABLE notifications.deliveries
    ADD CONSTRAINT deliveries_status_check CHECK (status IN ('pending', 'sent', 'failed'));
