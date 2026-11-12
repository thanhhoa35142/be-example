-- Domain: devices, reports, crowd reports, score feedback, alerts

CREATE TABLE device (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    apns_token    TEXT UNIQUE,
    apns_env      TEXT,
    locale        TEXT,
    tz            TEXT,
    app_version   TEXT,
    prefs         JSONB NOT NULL DEFAULT '{}',
    attest_key_id TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ
    -- no location columns
);

CREATE TABLE spot_report (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    spot_id     UUID NOT NULL REFERENCES spot(id),
    device_id   UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    type        TEXT NOT NULL CHECK (type IN ('confirm', 'missing', 'closed', 'issue')),
    issue_code  TEXT,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'accepted', 'rejected', 'duplicate')),
    created_day DATE NOT NULL DEFAULT current_date,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, spot_id, type, created_day)
);

CREATE TABLE crowd_report (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    spot_id      UUID NOT NULL REFERENCES spot(id),
    device_id    UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    level        SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 3),
    local_dow    SMALLINT NOT NULL,
    local_hour   SMALLINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE score_feedback (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    spot_id    UUID NOT NULL,
    ts_hour    TIMESTAMPTZ NOT NULL,
    device_id  UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    verdict    SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE alert_rule (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    device_id    UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    spot_id      UUID REFERENCES spot(id),
    h3_cell      BIGINT,
    radius_m     INT,
    min_score    SMALLINT NOT NULL,
    days_mask    SMALLINT NOT NULL,
    start_local  TIME NOT NULL,
    end_local    TIME NOT NULL,
    tz           TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    CHECK (spot_id IS NOT NULL OR h3_cell IS NOT NULL)
);

CREATE TABLE alert_delivery (
    rule_id       UUID REFERENCES alert_rule(id) ON DELETE CASCADE,
    window_start  TIMESTAMPTZ,
    sent_at       TIMESTAMPTZ,
    apns_status   INT,
    PRIMARY KEY (rule_id, window_start)
);
