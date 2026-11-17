-- C2 push outbox: plan.changed / plan.confirmed, one row per (plan, device,
-- local day of the plan's timezone), so a device gets at most one push per
-- plan per day. Rows go with the plan (cascade), with the device
-- (SoftDeleteDevice) and with the plan's 30-day retention purge.
CREATE TABLE t_plan_deliveries (
    plan_id     uuid        NOT NULL,
    device_id   uuid        NOT NULL,
    local_date  date        NOT NULL,
    kind        text        NOT NULL,
    -- plans.Notification: language-neutral facts; the APNs body (loc keys)
    -- is built at send time.
    payload     jsonb       NOT NULL,
    status      text        DEFAULT 'pending' NOT NULL,
    attempts    smallint    DEFAULT 0 NOT NULL,
    created_at  timestamptz DEFAULT now() NOT NULL,
    claimed_at  timestamptz,
    sent_at     timestamptz,
    apns_status smallint,
    last_error  text,
    CONSTRAINT pk_t_plan_deliveries            PRIMARY KEY (plan_id, device_id, local_date),
    CONSTRAINT fk_t_plan_deliveries_t_plans    FOREIGN KEY (plan_id)   REFERENCES t_plans (id)   ON DELETE CASCADE,
    CONSTRAINT fk_t_plan_deliveries_t_devices  FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_plan_deliveries_kind       CHECK (kind IN ('plan.changed','plan.confirmed')),
    CONSTRAINT ck_t_plan_deliveries_status     CHECK (status IN ('pending','claimed','sent','failed','dead')),
    CONSTRAINT ck_t_plan_deliveries_sent_pair  CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);
CREATE INDEX ix_t_plan_deliveries_open   ON t_plan_deliveries (created_at) WHERE status IN ('pending','claimed','failed');
CREATE INDEX ix_t_plan_deliveries_device ON t_plan_deliveries (device_id);

INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('plans.evaluate', 900, true, 360)
ON CONFLICT (job) DO NOTHING;
