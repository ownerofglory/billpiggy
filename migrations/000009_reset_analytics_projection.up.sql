-- Clear the analytics read model so it can be rebuilt from the event log.
--
-- The previous projector could leave partial state: it reversed a contribution
-- and then returned early for event types it did not recognise, and it rolled a
-- whole 50-event batch back on any single failure. The rollups are therefore
-- untrustworthy and are rebuilt rather than repaired.
--
-- Replay itself is not a migration. The application enqueues outbox rows for
-- historical events when it registers a subscription that has never run, so a
-- newly added projection backfills through exactly the same code path as live
-- traffic, with Message.Replay set to suppress user-visible side effects.

TRUNCATE TABLE analytics.tag_expense_rollups;
TRUNCATE TABLE analytics.expense_rollups;
TRUNCATE TABLE analytics.expense_contributions;
TRUNCATE TABLE budgets.expense_contributions;
TRUNCATE TABLE budgets.budget_usage;
