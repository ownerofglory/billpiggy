-- Self-service password reset. Follows identity.invitations: only a hash of
-- the emailed token is ever stored, and a reset is redeemable once, within a
-- short window, before it is marked used.

CREATE TABLE identity.password_resets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES identity.users(id),
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at TIMESTAMPTZ
);

-- Serves InvalidatePendingPasswordResets, which closes out every other
-- outstanding reset for an account once one is used or the password is
-- changed directly. Partial, since a used reset is never looked up by user
-- again.
CREATE INDEX identity_password_resets_pending_idx
    ON identity.password_resets (user_id) WHERE used_at IS NULL;
