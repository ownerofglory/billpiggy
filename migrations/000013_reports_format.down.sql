ALTER TABLE reports.reports DROP CONSTRAINT reports_owner_period_format_key;
ALTER TABLE reports.reports ADD CONSTRAINT reports_owner_id_period_kind_period_start_key UNIQUE (owner_id, period_kind, period_start);
ALTER TABLE reports.reports DROP COLUMN format;
