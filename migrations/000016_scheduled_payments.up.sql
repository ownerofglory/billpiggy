-- Scheduled (recurring) payments: rent, insurance premiums, subscriptions.
--
-- A row is a template plus a cursor. The template describes what to charge
-- and how often; next_due_at is the occurrence the scheduler has not handled
-- yet, and start_date stays fixed as the recurrence anchor so a payment due
-- on the 31st returns to the 31st after landing on a short month's last day.

CREATE SCHEMA IF NOT EXISTS payments;

CREATE TABLE payments.scheduled_payments (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    title TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL,
    category_id UUID REFERENCES expenses.categories(id),
    category_name TEXT,
    -- Tags are stored inline rather than through a join table like
    -- expenses.expense_tags: nothing queries scheduled payments by tag, they
    -- are only copied onto each posted expense, which has its own join rows.
    tag_ids UUID[] NOT NULL DEFAULT '{}',
    shared_group_id UUID REFERENCES identity.groups(id),
    frequency TEXT NOT NULL CHECK (frequency IN ('monthly', 'quarterly', 'yearly', 'custom')),
    -- Only meaningful for the custom frequency, which counts in days so it can
    -- express the intervals the month-based frequencies cannot (weekly,
    -- fortnightly, every 45 days).
    custom_interval_days INTEGER NOT NULL DEFAULT 0 CHECK (custom_interval_days BETWEEN 0 AND 3650),
    start_date TIMESTAMPTZ NOT NULL,
    end_date TIMESTAMPTZ,
    next_due_at TIMESTAMPTZ NOT NULL,
    last_posted_at TIMESTAMPTZ,
    auto_post BOOLEAN NOT NULL DEFAULT TRUE,
    reminder_days_before SMALLINT NOT NULL DEFAULT 0 CHECK (reminder_days_before BETWEEN 0 AND 60),
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT scheduled_payments_custom_interval_required
        CHECK (frequency <> 'custom' OR custom_interval_days >= 1),
    CONSTRAINT scheduled_payments_end_after_start
        CHECK (end_date IS NULL OR end_date >= start_date)
);

-- Serves the scheduler's only query: the next batch of live payments that
-- have come due. Partial, since paused and deleted rows are never scanned.
CREATE INDEX scheduled_payments_due_idx
    ON payments.scheduled_payments (next_due_at)
    WHERE deleted_at IS NULL AND paused = FALSE;

CREATE INDEX scheduled_payments_owner_idx
    ON payments.scheduled_payments (owner_id)
    WHERE deleted_at IS NULL;

-- The ledger of occurrences already handled. Its primary key is the only
-- thing stopping two replicas' schedulers from posting the same rent twice:
-- both insert, one wins, the loser sees zero rows affected and stands down.
-- Keeping the reminder and the due occurrence as separate kinds lets each be
-- claimed exactly once without a second table.
CREATE TABLE payments.scheduled_payment_postings (
    scheduled_payment_id UUID NOT NULL REFERENCES payments.scheduled_payments(id) ON DELETE CASCADE,
    due_at TIMESTAMPTZ NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('reminder', 'due')),
    expense_id UUID REFERENCES expenses.expenses(id),
    posted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scheduled_payment_id, due_at, kind)
);
