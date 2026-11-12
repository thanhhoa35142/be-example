-- Domain: weather, scoring, crowd estimates
-- Partitioned parents get a DEFAULT partition for dev; production partition
-- management (pg_partman, daily partitions, 14-day retention) is an ops task.

CREATE TABLE weather_hourly (
    cell_id     BIGINT NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    temp        REAL,
    feels_like  REAL,
    precip_prob SMALLINT,
    wind        REAL,
    uv          REAL,
    cloud       SMALLINT,
    provider    TEXT NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (cell_id, ts)
) PARTITION BY RANGE (ts);

CREATE TABLE weather_hourly_default PARTITION OF weather_hourly DEFAULT;

CREATE TABLE scoring_config (
    version       SMALLINT PRIMARY KEY,
    weights       JSONB NOT NULL,
    thresholds    JSONB NOT NULL,
    active_from   TIMESTAMPTZ NOT NULL
);

CREATE TABLE spot_score (
    spot_id        UUID NOT NULL,
    ts_hour        TIMESTAMPTZ NOT NULL,
    config_version SMALLINT NOT NULL,
    score          SMALLINT NOT NULL CHECK (score BETWEEN 0 AND 100),
    s_weather      SMALLINT,
    s_crowd        SMALLINT,
    s_amenity      SMALLINT,
    s_shade        SMALLINT,
    s_fresh        SMALLINT,
    cap_reason     TEXT,
    reasons        JSONB NOT NULL,
    confidence     SMALLINT NOT NULL CHECK (confidence BETWEEN 1 AND 3),
    computed_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (spot_id, ts_hour)
) PARTITION BY RANGE (ts_hour);

CREATE TABLE spot_score_default PARTITION OF spot_score DEFAULT;

CREATE TABLE crowd_curve (
    kind            TEXT NOT NULL,
    dow             SMALLINT NOT NULL,
    hour            SMALLINT NOT NULL,
    factor          REAL NOT NULL,
    config_version  SMALLINT NOT NULL,
    PRIMARY KEY (kind, dow, hour, config_version)
);

CREATE TABLE crowd_estimate (
    spot_id      UUID NOT NULL,
    dow          SMALLINT NOT NULL,
    hour         SMALLINT NOT NULL,
    level        SMALLINT NOT NULL,
    source       TEXT NOT NULL CHECK (source IN ('model', 'community', 'provider')),
    n_reports    INT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (spot_id, dow, hour)
);
