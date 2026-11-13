DELETE FROM t_worker_job_schedules WHERE job = 'holidays.refresh';
DROP INDEX IF EXISTS ux_t_holidays_country_region_date_kind;
