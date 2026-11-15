-- Weekly weekend digest (Plus): one row per device with the spots to
-- summarise. send_dow is time.Weekday (0=Sunday); send_hour is device-local.
-- last_sent_on is the device-local date of the last send/skip (idempotency).
CREATE TABLE t_weekly_digests (
    device_id    uuid        NOT NULL,
    spot_ids     uuid[]      NOT NULL,
    enabled      boolean     NOT NULL DEFAULT true,
    send_dow     smallint    NOT NULL DEFAULT 4,
    send_hour    smallint    NOT NULL DEFAULT 18,
    last_sent_on date,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_weekly_digests PRIMARY KEY (device_id),
    CONSTRAINT fk_t_weekly_digests_t_devices FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_weekly_digests_spot_ids CHECK (cardinality(spot_ids) <= 20),
    CONSTRAINT ck_t_weekly_digests_send_dow CHECK (send_dow BETWEEN 0 AND 6),
    CONSTRAINT ck_t_weekly_digests_send_hour CHECK (send_hour BETWEEN 0 AND 23)
);
CREATE INDEX ix_t_weekly_digests_enabled ON t_weekly_digests (device_id) WHERE enabled;

-- Hourly tick: each run only sends to devices whose local time is send_dow/send_hour.
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('digest.weekly', 3600, true, 300)
ON CONFLICT (job) DO NOTHING;
