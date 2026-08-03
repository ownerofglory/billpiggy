CREATE TABLE analytics.expense_contributions (
    expense_id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES identity.users(id),
    category_id UUID REFERENCES expenses.categories(id),
    tag_ids UUID[] NOT NULL DEFAULT '{}',
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor >= 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX analytics_expense_contributions_owner_idx
    ON analytics.expense_contributions (owner_id, occurred_at DESC) WHERE active;
