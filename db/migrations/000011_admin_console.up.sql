-- Admin console: "run now" for worker jobs. The API only records the
-- request; a running worker claims it on its next schedule poll (<= 30 s)
-- and executes the job under the usual advisory lock.
ALTER TABLE t_worker_job_schedules
    ADD COLUMN run_requested_at timestamptz;
