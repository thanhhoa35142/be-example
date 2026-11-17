-- Per-pack ingest run requests: the admin console can ask the worker to run
-- ingest.osm for a single city pack instead of every stale pack. The slug is
-- claimed atomically together with run_requested_at and cleared on claim.
ALTER TABLE t_worker_job_schedules
    ADD COLUMN run_requested_pack text;
