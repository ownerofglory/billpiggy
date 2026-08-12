-- Only removes defaults no expense or budget still references, so rolling back
-- can never orphan a projection row. Mirrors 000008's down migration.
DELETE FROM expenses.categories c
 WHERE c.owner_id IS NULL
   AND c.is_default
   AND c.id IN (
       '0197c1a0-0000-4000-8000-000000000009',
       '0197c1a0-0000-4000-8000-00000000000a',
       '0197c1a0-0000-4000-8000-00000000000b',
       '0197c1a0-0000-4000-8000-00000000000c',
       '0197c1a0-0000-4000-8000-00000000000d',
       '0197c1a0-0000-4000-8000-00000000000e',
       '0197c1a0-0000-4000-8000-00000000000f'
   )
   AND NOT EXISTS (SELECT 1 FROM expenses.expenses e WHERE e.category_id = c.id)
   AND NOT EXISTS (SELECT 1 FROM budgets.budgets b WHERE b.category_id = c.id);
