CREATE TABLE worker_job_schedule (
    job              TEXT PRIMARY KEY,
    interval_seconds INT NOT NULL CHECK (interval_seconds BETWEEN 60 AND 2592000),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO worker_job_schedule (job, interval_seconds) VALUES
    ('weather.refresh', 3600),
    ('crowd.refresh', 86400),
    ('crowd.aggregate', 3600),
    ('scoring.recompute', 3600),
    ('alerts.evaluate', 900);
