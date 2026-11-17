-- Good-day push (docs/GOOD_DAY_PUSH_SPEC.md §5).
--
-- t_nudge_subscriptions: opt-in + home area per device. The home is an H3
-- res-7 cell (~5 km²); raw coordinates are never stored.
CREATE TABLE t_nudge_subscriptions (
    device_id       uuid        NOT NULL,
    enabled         boolean     NOT NULL DEFAULT true,
    home_cell       bigint,
    home_source     text,
    home_updated_at timestamptz,
    send_hour       smallint    NOT NULL DEFAULT 8,
    consent         text        NOT NULL,
    ignored_streak  smallint    NOT NULL DEFAULT 0,
    last_sent_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_nudge_subscriptions PRIMARY KEY (device_id),
    CONSTRAINT fk_t_nudge_subscriptions_t_devices FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_nudge_subscriptions_source CHECK (home_source IN ('location','city')),
    -- A home is all or nothing: the app may opt in before it has a location fix.
    CONSTRAINT ck_t_nudge_subscriptions_home CHECK ((home_cell IS NULL) = (home_source IS NULL) AND (home_cell IS NULL) = (home_updated_at IS NULL)),
    CONSTRAINT ck_t_nudge_subscriptions_consent CHECK (consent IN ('explicit','provisional')),
    CONSTRAINT ck_t_nudge_subscriptions_send_hour CHECK (send_hour BETWEEN 7 AND 10),
    CONSTRAINT ck_t_nudge_subscriptions_streak CHECK (ignored_streak >= 0)
);
CREATE INDEX ix_t_nudge_subscriptions_cell ON t_nudge_subscriptions (home_cell) WHERE enabled;

-- Idempotency: one nudge per device per local day (claim -> sent/failed/dead).
CREATE TABLE t_nudge_deliveries (
    device_id    uuid        NOT NULL,
    local_date   date        NOT NULL,   -- local date of the send (not the target day)
    slot         text        NOT NULL,   -- 'day' | 'weekend'
    target_date  date        NOT NULL,
    spot_id      uuid        NOT NULL,
    window_start timestamptz NOT NULL,
    window_end   timestamptz NOT NULL,
    score        smallint    NOT NULL,
    status       text        NOT NULL DEFAULT 'claimed',
    attempts     smallint    NOT NULL DEFAULT 0,
    claimed_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz,
    apns_status  integer,
    last_error   text,
    CONSTRAINT pk_t_nudge_deliveries PRIMARY KEY (device_id, local_date),
    CONSTRAINT fk_t_nudge_deliveries_t_devices FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_nudge_deliveries_slot CHECK (slot IN ('day','weekend')),
    CONSTRAINT ck_t_nudge_deliveries_status CHECK (status IN ('claimed','sent','failed','dead')),
    CONSTRAINT ck_t_nudge_deliveries_sent_pair CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);

-- Cross-kind frequency cap. Every sender (alerts, digest, nudge) inserts a row
-- after a successful APNs 200. Purged after 30 days by retention.purge.
CREATE TABLE t_push_sends (
    device_id uuid        NOT NULL,
    kind      text        NOT NULL,
    sent_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_t_push_sends_t_devices FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE
);
CREATE INDEX ix_t_push_sends_device_time ON t_push_sends (device_id, sent_at DESC);
CREATE INDEX ix_t_push_sends_sent_at ON t_push_sends (sent_at);

-- Tick every 15 minutes: each run only handles devices whose local time is
-- the send hour (day slot) or Friday 18:00 (weekend slot). The worker only
-- registers the job when NUDGE_ENABLED=true.
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('nudge.goodday', 900, true, 120)
ON CONFLICT (job) DO NOTHING;
