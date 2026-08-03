-- Seed the categories the application ships for every user.
--
-- expenses.categories.owner_id IS NULL marks a system default, and the table's
-- UNIQUE NULLS NOT DISTINCT (owner_id, name) constraint makes the insert
-- idempotent. The identifiers must stay in step with domain.DefaultCategories.

INSERT INTO expenses.categories (id, owner_id, name, color, is_default)
VALUES
    ('0197c1a0-0000-4000-8000-000000000001', NULL, 'Food',          '#f97316', true),
    ('0197c1a0-0000-4000-8000-000000000002', NULL, 'Groceries',     '#84cc16', true),
    ('0197c1a0-0000-4000-8000-000000000003', NULL, 'Transport',     '#0ea5e9', true),
    ('0197c1a0-0000-4000-8000-000000000004', NULL, 'Home',          '#8b5cf6', true),
    ('0197c1a0-0000-4000-8000-000000000005', NULL, 'Utilities',     '#14b8a6', true),
    ('0197c1a0-0000-4000-8000-000000000006', NULL, 'Health',        '#ef4444', true),
    ('0197c1a0-0000-4000-8000-000000000007', NULL, 'Entertainment', '#ec4899', true),
    ('0197c1a0-0000-4000-8000-000000000008', NULL, 'Other',         '#64748b', true)
ON CONFLICT (id) DO UPDATE
    SET name = excluded.name,
        color = excluded.color,
        is_default = true;
