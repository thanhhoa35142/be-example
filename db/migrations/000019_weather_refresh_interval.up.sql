-- weather.refresh every 3h: the Open-Meteo daily quota (9000 calls/day by
-- default) is charged per cell per refresh, so hourly runs cap sustainable
-- coverage at ~375 cells. 8 runs/day raises that to ~1100 cells.
UPDATE t_worker_job_schedules
SET interval_seconds = 10800
WHERE job = 'weather.refresh';
