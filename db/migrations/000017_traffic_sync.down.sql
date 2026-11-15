DELETE FROM t_worker_job_schedules WHERE job = 'google.sync';

DROP TABLE IF EXISTS t_traffic_ga4_daily;
DROP TABLE IF EXISTS t_traffic_gsc_dims;
DROP TABLE IF EXISTS t_traffic_gsc_daily;

ALTER TABLE t_admin_google_tokens
    DROP COLUMN IF EXISTS ga4_property_id,
    DROP COLUMN IF EXISTS gsc_site_url;
