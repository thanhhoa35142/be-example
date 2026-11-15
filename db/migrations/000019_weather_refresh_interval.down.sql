UPDATE t_worker_job_schedules
SET interval_seconds = 3600
WHERE job = 'weather.refresh';
