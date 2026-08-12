-- Extend the categories every user starts with (migration 000008) with a few
-- more common household expense categories, plus Investments (not really an
-- expense, but worth tracking the same way), without editing 000008 itself,
-- since it has almost certainly already run against every existing database.
--
-- expenses.categories.owner_id IS NULL marks a system default, and the table's
-- UNIQUE NULLS NOT DISTINCT (owner_id, name) constraint makes the insert
-- idempotent. The identifiers must stay in step with domain.DefaultCategories.

INSERT INTO expenses.categories (id, owner_id, name, color, is_default)
VALUES
    ('0197c1a0-0000-4000-8000-000000000009', NULL, 'Pets',          '#f59e0b', true),
    ('0197c1a0-0000-4000-8000-00000000000a', NULL, 'Education',     '#6366f1', true),
    ('0197c1a0-0000-4000-8000-00000000000b', NULL, 'Insurance',     '#3b82f6', true),
    ('0197c1a0-0000-4000-8000-00000000000c', NULL, 'Travel',        '#06b6d4', true),
    ('0197c1a0-0000-4000-8000-00000000000d', NULL, 'Personal Care', '#d946ef', true),
    ('0197c1a0-0000-4000-8000-00000000000e', NULL, 'Investments',   '#22c55e', true),
    ('0197c1a0-0000-4000-8000-00000000000f', NULL, 'Shopping',      '#eab308', true)
ON CONFLICT (id) DO UPDATE
    SET name = excluded.name,
        color = excluded.color,
        is_default = true;
