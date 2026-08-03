-- Only removes defaults no expense or budget still references, so rolling back
-- can never orphan a projection row.
DELETE FROM expenses.categories c
 WHERE c.owner_id IS NULL
   AND c.is_default
   AND NOT EXISTS (SELECT 1 FROM expenses.expenses e WHERE e.category_id = c.id)
   AND NOT EXISTS (SELECT 1 FROM budgets.budgets b WHERE b.category_id = c.id);
