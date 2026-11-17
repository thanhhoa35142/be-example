-- C2 of FEATURE_PLAN_DECISION_PLANS_GRASS.md: server-stored picnic plans.
-- First table that records "this device will be at this place at this time":
-- rows are deleted with the device (SoftDeleteDevice) and purged 30 days after
-- the plan ended or was canceled (retention.purge).

CREATE TABLE t_plans (
    id               uuid        DEFAULT uuidv7() NOT NULL,
    -- Unguessable capability for followers and the /p/ page: 16 bytes from
    -- crypto/rand, base64url without padding (22 chars).
    share_token      text        NOT NULL,
    device_id        uuid        NOT NULL,
    spot_id          uuid        NOT NULL,
    start_ts         timestamptz NOT NULL,
    end_ts           timestamptz NOT NULL,
    tz               text        NOT NULL,
    -- Window mean at creation (or at the first evaluation that had scores).
    baseline_score   smallint,
    -- Latest evaluation (plans.evaluate): ok | worse | bad, window mean,
    -- first rain/heat/cold cap in the window, best alternative.
    state            text,
    score            smallint,
    cap_reason       text,
    alternative      jsonb,
    evaluated_at     timestamptz,
    -- Last state pushed to owner and followers (or set silently as the
    -- starting point). state <> notified_state = a change waiting to be sent.
    notified_state   text,
    last_notified_on date,
    last_notified_at timestamptz,
    confirmed_at     timestamptz,
    canceled_at     timestamptz,
    created_at       timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_plans                 PRIMARY KEY (id),
    CONSTRAINT uq_t_plans_share_token     UNIQUE (share_token),
    CONSTRAINT fk_t_plans_t_devices       FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_plans_t_spots         FOREIGN KEY (spot_id)   REFERENCES t_spots (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_plans_window          CHECK (end_ts > start_ts AND end_ts - start_ts <= interval '12 hours'),
    CONSTRAINT ck_t_plans_token           CHECK (share_token ~ '^[A-Za-z0-9_-]{22}$'),
    CONSTRAINT ck_t_plans_state           CHECK (state IS NULL OR state IN ('ok','worse','bad')),
    CONSTRAINT ck_t_plans_notified_state  CHECK (notified_state IS NULL OR notified_state IN ('ok','worse','bad')),
    CONSTRAINT ck_t_plans_baseline        CHECK (baseline_score IS NULL OR baseline_score BETWEEN 0 AND 100),
    CONSTRAINT ck_t_plans_score           CHECK (score IS NULL OR score BETWEEN 0 AND 100)
);
CREATE INDEX ix_t_plans_device   ON t_plans (device_id, end_ts);
CREATE INDEX ix_t_plans_upcoming ON t_plans (start_ts) WHERE canceled_at IS NULL;

CREATE TABLE t_plan_followers (
    plan_id    uuid        NOT NULL,
    device_id  uuid        NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_plan_followers           PRIMARY KEY (plan_id, device_id),
    CONSTRAINT fk_t_plan_followers_t_plans   FOREIGN KEY (plan_id)   REFERENCES t_plans (id)   ON DELETE CASCADE,
    CONSTRAINT fk_t_plan_followers_t_devices FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE
);
CREATE INDEX ix_t_plan_followers_device ON t_plan_followers (device_id);

-- Display/admin parity with alert_rules. The enforced caps are plans.Limits
-- (code), like alerts.Limits.
INSERT INTO t_plan_limits (plan, feature, limit_value) VALUES
    ('free', 'active_plans', 1),
    ('plus', 'active_plans', 20)
ON CONFLICT (plan, feature) DO NOTHING;
