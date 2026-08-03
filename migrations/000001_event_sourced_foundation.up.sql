CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS events;
CREATE SCHEMA IF NOT EXISTS identity;
CREATE SCHEMA IF NOT EXISTS expenses;
CREATE SCHEMA IF NOT EXISTS budgets;
CREATE SCHEMA IF NOT EXISTS analytics;
CREATE SCHEMA IF NOT EXISTS reports;
CREATE SCHEMA IF NOT EXISTS notifications;
CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE events.events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_version BIGINT NOT NULL CHECK (aggregate_version > 0),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    correlation_id UUID,
    causation_id UUID,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (aggregate_type, aggregate_id, aggregate_version)
);

CREATE INDEX events_events_aggregate_idx
    ON events.events (aggregate_type, aggregate_id, aggregate_version);
CREATE INDEX events_events_occurred_at_idx ON events.events (occurred_at DESC);
CREATE INDEX events_events_type_idx ON events.events (event_type, occurred_at DESC);

CREATE TABLE events.outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL UNIQUE REFERENCES events.events(id),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    locked_at TIMESTAMPTZ,
    locked_by TEXT,
    processed_at TIMESTAMPTZ,
    last_error TEXT
);

CREATE INDEX events_outbox_pending_idx
    ON events.outbox (available_at, id) WHERE processed_at IS NULL;

CREATE TABLE events.projector_checkpoints (
    projector_name TEXT PRIMARY KEY,
    last_event_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE identity.user_role AS ENUM ('member', 'admin', 'super_admin');
CREATE TYPE identity.invitation_status AS ENUM ('pending', 'accepted', 'revoked', 'expired');

CREATE TABLE identity.users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    profile_image_object_key TEXT,
    role identity.user_role NOT NULL DEFAULT 'member',
    access_blocked BOOLEAN NOT NULL DEFAULT false,
    email_notifications_enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE identity.refresh_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES identity.users(id),
    token_hash BYTEA NOT NULL UNIQUE,
    family_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    replaced_by UUID
);

CREATE INDEX identity_refresh_tokens_active_idx
    ON identity.refresh_tokens (user_id, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE identity.invitations (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL,
    role identity.user_role NOT NULL DEFAULT 'member',
    token_hash BYTEA NOT NULL UNIQUE,
    status identity.invitation_status NOT NULL DEFAULT 'pending',
    invited_by UUID NOT NULL REFERENCES identity.users(id),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_by UUID REFERENCES identity.users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ
);

CREATE TABLE identity.groups (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES identity.users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (created_by, name)
);

CREATE TABLE identity.group_members (
    group_id UUID NOT NULL REFERENCES identity.groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES identity.users(id),
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);

CREATE TYPE expenses.expense_status AS ENUM ('draft', 'confirmed', 'shared', 'reimbursed', 'voided');

CREATE TABLE expenses.categories (
    id UUID PRIMARY KEY,
    owner_id UUID REFERENCES identity.users(id),
    name TEXT NOT NULL,
    color TEXT,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (owner_id, name)
);

CREATE TABLE expenses.tags (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    name TEXT NOT NULL,
    color TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

CREATE TABLE expenses.expenses (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    title TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor >= 0),
    currency CHAR(3) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    category_id UUID REFERENCES expenses.categories(id),
    status expenses.expense_status NOT NULL DEFAULT 'confirmed',
    shared_group_id UUID REFERENCES identity.groups(id),
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    address TEXT,
    receipt_object_key TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX expenses_expenses_owner_occurred_idx
    ON expenses.expenses (owner_id, occurred_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX expenses_expenses_category_idx
    ON expenses.expenses (owner_id, category_id, occurred_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE expenses.expense_tags (
    expense_id UUID NOT NULL REFERENCES expenses.expenses(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES expenses.tags(id),
    PRIMARY KEY (expense_id, tag_id)
);

CREATE TABLE expenses.expense_items (
    id UUID PRIMARY KEY,
    expense_id UUID NOT NULL REFERENCES expenses.expenses(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    quantity NUMERIC(12, 3) NOT NULL DEFAULT 1,
    amount_minor BIGINT NOT NULL CHECK (amount_minor >= 0),
    position INTEGER NOT NULL CHECK (position >= 0),
    UNIQUE (expense_id, position)
);

CREATE TABLE budgets.budgets (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    category_id UUID REFERENCES expenses.categories(id),
    name TEXT NOT NULL,
    amount_limit_minor BIGINT NOT NULL CHECK (amount_limit_minor > 0),
    currency CHAR(3) NOT NULL,
    threshold_percent SMALLINT NOT NULL CHECK (threshold_percent BETWEEN 1 AND 100),
    due_at TIMESTAMPTZ,
    period TEXT NOT NULL CHECK (period IN ('daily', 'weekly', 'monthly', 'yearly', 'custom')),
    shared_group_id UUID REFERENCES identity.groups(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE budgets.budget_usage (
    budget_id UUID NOT NULL REFERENCES budgets.budgets(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    spent_minor BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (budget_id, period_start)
);

CREATE TABLE analytics.expense_rollups (
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    period_start DATE NOT NULL,
    period_kind TEXT NOT NULL CHECK (period_kind IN ('day', 'week', 'month', 'year')),
    category_id UUID REFERENCES expenses.categories(id),
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL DEFAULT 0,
    expense_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (owner_id, period_start, period_kind, category_id, currency)
);

CREATE TABLE reports.reports (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    period_kind TEXT NOT NULL CHECK (period_kind IN ('week', 'month', 'year')),
    period_start DATE NOT NULL,
    object_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, period_kind, period_start)
);

CREATE TABLE notifications.deliveries (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES identity.users(id),
    kind TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    failure_reason TEXT
);

CREATE TABLE audit.entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID REFERENCES identity.users(id),
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_entries_resource_idx ON audit.entries (resource_type, resource_id, occurred_at DESC);
