-- Holidays reference data: one row per (country, region, date, kind) so the
-- holidays.refresh job can upsert idempotently. Region '' = national.
CREATE UNIQUE INDEX IF NOT EXISTS ux_t_holidays_country_region_date_kind
    ON t_holidays (country, region, date, kind);

-- Weekly refresh; offset 12 min keeps it clear of the other boot-time jobs.
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('holidays.refresh', 604800, true, 720)
ON CONFLICT (job) DO NOTHING;
