-- Reports ship as CSV and PDF, so one period now produces two rows sharing
-- the same period_kind/period_start, distinguished by format.

ALTER TABLE reports.reports
    ADD COLUMN format TEXT NOT NULL DEFAULT 'csv' CHECK (format IN ('csv', 'pdf'));

ALTER TABLE reports.reports ALTER COLUMN format DROP DEFAULT;

ALTER TABLE reports.reports
    DROP CONSTRAINT reports_owner_id_period_kind_period_start_key;

ALTER TABLE reports.reports
    ADD CONSTRAINT reports_owner_period_format_key UNIQUE (owner_id, period_kind, period_start, format);
