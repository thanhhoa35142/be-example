DELETE FROM t_worker_job_schedules WHERE job = 'digest.weekly';
DROP TABLE IF EXISTS t_weekly_digests;
