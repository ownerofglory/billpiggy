-- Per-user opt-out for AI features. Defaulting to true keeps existing users'
-- behavior unchanged; a user who wants no AI use at all can turn it off from
-- their profile.
ALTER TABLE identity.users
    ADD COLUMN IF NOT EXISTS ai_enabled BOOLEAN NOT NULL DEFAULT true;

CREATE SCHEMA IF NOT EXISTS ai;

-- Cost/audit trail for every AI provider call. This is intentionally separate
-- from audit.entries: it is written directly by the calling service rather
-- than projected from a domain event, since an AI request is an external side
-- effect with a cost, not a state change to an aggregate.
CREATE TABLE ai.requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES identity.users(id),
    workload TEXT NOT NULL CHECK (workload IN
        ('assistant', 'receipt_extraction', 'sentence_extraction', 'transcription')),
    model TEXT NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    total_tokens BIGINT NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'error')),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ai_requests_user_created_idx ON ai.requests (user_id, created_at DESC);
CREATE INDEX ai_requests_workload_created_idx ON ai.requests (workload, created_at DESC);
