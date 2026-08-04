-- Per-kind notification preferences, layered on top of the existing
-- email_notifications_enabled master switch: a key present here overrides
-- the switch for that notification kind; a key absent follows it.

ALTER TABLE identity.users
    ADD COLUMN notification_preferences JSONB NOT NULL DEFAULT '{}'::jsonb;
