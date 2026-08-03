CREATE TABLE analytics.tag_expense_rollups (
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    period_start DATE NOT NULL,
    period_kind TEXT NOT NULL CHECK (period_kind IN ('day', 'week', 'month', 'year')),
    tag_id UUID NOT NULL REFERENCES expenses.tags(id),
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL DEFAULT 0,
    expense_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (owner_id, period_start, period_kind, tag_id, currency)
);
