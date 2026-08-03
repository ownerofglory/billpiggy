-- Give the budgets context its own expense mirror and the usage bookkeeping the
-- threshold alerts need.

-- The budgets context does not read analytics.expense_contributions: bounded
-- contexts do not query one another's write models. A small duplicated mirror
-- is the correct price for that isolation.
CREATE TABLE budgets.expense_contributions (
    expense_id UUID PRIMARY KEY,
    owner_id UUID NOT NULL,
    category_id UUID,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX budgets_expense_contributions_window_idx
    ON budgets.expense_contributions (owner_id, category_id, occurred_at)
    WHERE active;

-- Highest threshold already alerted on, so a budget hovering at its limit does
-- not email the owner on every single expense.
ALTER TABLE budgets.budget_usage
    ADD COLUMN IF NOT EXISTS alerted_percent SMALLINT NOT NULL DEFAULT 0
        CHECK (alerted_percent BETWEEN 0 AND 100);
