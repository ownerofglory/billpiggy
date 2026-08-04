-- A durable, cross-replica fixed-window rate limiter. The in-memory limiter
-- resets on restart and is independent per pod; this table gives every
-- replica a shared view of the same window.

CREATE SCHEMA IF NOT EXISTS ratelimit;

CREATE TABLE ratelimit.windows (
    key TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    count INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
    PRIMARY KEY (key, window_start)
);

-- Supports the periodic cleanup of closed windows; without it the table grows
-- forever since a window is never explicitly deleted when it closes.
CREATE INDEX ratelimit_windows_window_start_idx ON ratelimit.windows (window_start);
