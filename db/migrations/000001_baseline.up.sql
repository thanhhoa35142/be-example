-- ============================================================================
-- Pickhour baseline schema (squashed migrations 1-13 → 0001)
-- PostgreSQL 18 + PostGIS 3.6. Dev-only rebuild; see docs/WORKFLOW.md.
--
-- Conventions: tables t_<plural>, constraints pk_/fk_/uq_/ck_/ix_,
-- functions fn_*, triggers trg_*. schema_migrations is golang-migrate's.
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS postgis;

-- ---------------------------------------------------------------------------
-- Shared helpers
-- ---------------------------------------------------------------------------

-- fn_assert_transition raises check_violation (23514) when p_to is not in
-- p_allowed. Used by all strict state-machine triggers.
CREATE FUNCTION fn_assert_transition(p_allowed text[], p_from text, p_to text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF p_from IS NOT DISTINCT FROM p_to THEN
        RETURN;
    END IF;
    IF NOT (p_to = ANY (p_allowed)) THEN
        RAISE EXCEPTION 'invalid state transition: % -> %', p_from, p_to
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

-- fn_log_transition is defined further down, after t_state_transitions
-- exists (SQL-language functions validate their body at creation time).

-- ---------------------------------------------------------------------------
-- Reference / geo
-- ---------------------------------------------------------------------------

CREATE TABLE t_city_packs (
    id               uuid        DEFAULT uuidv7() NOT NULL,
    slug             text        NOT NULL,
    name             text        NOT NULL,
    country          text        NOT NULL,
    tz               text        NOT NULL,
    boundary         geometry(MultiPolygon, 4326) NOT NULL,
    status           text        NOT NULL DEFAULT 'draft',
    osm_extract      text,
    last_import_at   timestamptz,
    CONSTRAINT pk_t_city_packs                    PRIMARY KEY (id),
    CONSTRAINT uq_t_city_packs_slug               UNIQUE (slug),
    CONSTRAINT ck_t_city_packs_country            CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT ck_t_city_packs_status             CHECK (status IN ('draft','beta','live','retired')),
    CONSTRAINT ck_t_city_packs_live_needs_import  CHECK (status NOT IN ('beta','live') OR last_import_at IS NOT NULL)
);
CREATE INDEX ix_t_city_packs_boundary ON t_city_packs USING gist (boundary);

CREATE TABLE t_spots (
    id               uuid        DEFAULT uuidv7() NOT NULL,
    city_pack_id     uuid        NOT NULL,
    kind             text        NOT NULL,
    name             text,
    names            jsonb       DEFAULT '{}'::jsonb NOT NULL,
    geom             geography(Point, 4326) NOT NULL,
    area             geometry(MultiPolygon, 4326),
    country          text        NOT NULL,
    city             text,
    tz               text        NOT NULL,
    cell_id          bigint      NOT NULL,
    status           text        NOT NULL DEFAULT 'pending',
    merged_into      uuid,
    last_verified_at timestamptz,
    created_at       timestamptz DEFAULT now() NOT NULL,
    updated_at       timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_spots                     PRIMARY KEY (id),
    CONSTRAINT fk_t_spots_t_city_packs        FOREIGN KEY (city_pack_id) REFERENCES t_city_packs (id),
    CONSTRAINT fk_t_spots_t_spots             FOREIGN KEY (merged_into)  REFERENCES t_spots (id),
    CONSTRAINT ck_t_spots_kind                CHECK (kind IN ('table','site','park','beach')),
    CONSTRAINT ck_t_spots_status              CHECK (status IN ('pending','active','closed','hidden','merged')),
    CONSTRAINT ck_t_spots_country             CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT ck_t_spots_merged_pair         CHECK ((status = 'merged') = (merged_into IS NOT NULL))
);
CREATE INDEX ix_t_spots_geom_gist        ON t_spots USING gist (geom);
CREATE INDEX ix_t_spots_area_gist        ON t_spots USING gist (area) WHERE area IS NOT NULL;
CREATE INDEX ix_t_spots_cell_id          ON t_spots (cell_id);
CREATE INDEX ix_t_spots_city_pack_status ON t_spots (city_pack_id, status);

CREATE TABLE t_spot_amenities (
    spot_id    uuid   NOT NULL,
    type       text   NOT NULL,
    distance_m smallint NOT NULL,
    source     text   NOT NULL,
    CONSTRAINT pk_t_spot_amenities      PRIMARY KEY (spot_id, type),
    CONSTRAINT fk_t_spot_amenities_t_spots FOREIGN KEY (spot_id) REFERENCES t_spots (id) ON DELETE CASCADE
);

CREATE TABLE t_spot_sources (
    spot_id     uuid        NOT NULL,
    source      text        NOT NULL,
    source_id   text        NOT NULL,
    version     integer,
    imported_at timestamptz NOT NULL,
    CONSTRAINT pk_t_spot_sources              PRIMARY KEY (source, source_id),
    CONSTRAINT fk_t_spot_sources_t_spots      FOREIGN KEY (spot_id) REFERENCES t_spots (id),
    CONSTRAINT ck_t_spot_sources_version      CHECK (version IS NULL OR version >= 0)
);
CREATE INDEX ix_t_spot_sources_spot_id ON t_spot_sources (spot_id);

-- ---------------------------------------------------------------------------
-- Devices / subscriptions / push
-- ---------------------------------------------------------------------------

CREATE TABLE t_devices (
    id                     uuid        DEFAULT uuidv7() NOT NULL,
    apns_token             text,
    apns_env               text,
    push_status            text        NOT NULL DEFAULT 'none',
    push_status_changed_at timestamptz,
    locale                 text,
    tz                     text,
    app_version            text,
    prefs                  jsonb       DEFAULT '{}'::jsonb NOT NULL,
    attest_key_id          text,
    created_at             timestamptz DEFAULT now() NOT NULL,
    last_seen_at           timestamptz,
    deleted_at             timestamptz,
    CONSTRAINT pk_t_devices                  PRIMARY KEY (id),
    CONSTRAINT uq_t_devices_apns_token       UNIQUE (apns_token),
    CONSTRAINT ck_t_devices_apns_env         CHECK (apns_env IS NULL OR apns_env IN ('sandbox','production')),
    CONSTRAINT ck_t_devices_push_status      CHECK (push_status IN ('none','active','unregistered')),
    CONSTRAINT ck_t_devices_push_token_pair  CHECK ((push_status = 'active') = (apns_token IS NOT NULL))
);
-- apns_token unique must ignore soft-deleted devices.
ALTER TABLE t_devices DROP CONSTRAINT uq_t_devices_apns_token;
CREATE UNIQUE INDEX uq_t_devices_apns_token
    ON t_devices (apns_token) WHERE deleted_at IS NULL AND apns_token IS NOT NULL;
CREATE INDEX ix_t_devices_push_active ON t_devices (id) WHERE push_status = 'active';

CREATE TABLE t_subscriptions (
    original_transaction_id text        NOT NULL,
    product_id              text        NOT NULL,
    status                  text        NOT NULL,
    environment             text        NOT NULL,
    ownership               text        NOT NULL,
    is_trial                boolean     DEFAULT false NOT NULL,
    auto_renew              boolean     DEFAULT true  NOT NULL,
    expires_at              timestamptz,
    grace_expires_at        timestamptz,
    revoked_at              timestamptz,
    last_signed_date        timestamptz NOT NULL,
    updated_at              timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_subscriptions               PRIMARY KEY (original_transaction_id),
    CONSTRAINT ck_t_subscriptions_status        CHECK (status IN ('active','grace','billing_retry','expired','revoked','refunded')),
    CONSTRAINT ck_t_subscriptions_environment   CHECK (environment IN ('production','sandbox')),
    CONSTRAINT ck_t_subscriptions_ownership     CHECK (ownership IN ('purchased','family_shared')),
    CONSTRAINT ck_t_subscriptions_grace_expiry  CHECK (status <> 'grace' OR grace_expires_at IS NOT NULL),
    CONSTRAINT ck_t_subscriptions_revoked_at    CHECK (status NOT IN ('revoked','refunded') OR revoked_at IS NOT NULL)
    -- Apple notifications can arrive out of order; updates are guarded in
    -- sqlc by `last_signed_date < $new_signed_date`, not by a trigger.
);

CREATE TABLE t_device_subscriptions (
    device_id                uuid        NOT NULL,
    original_transaction_id  text        NOT NULL,
    linked_at                timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_device_subscriptions            PRIMARY KEY (device_id, original_transaction_id),
    CONSTRAINT fk_t_device_subscriptions_t_devices   FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_device_subscriptions_t_subscriptions FOREIGN KEY (original_transaction_id) REFERENCES t_subscriptions (original_transaction_id)
);

CREATE TABLE t_apple_notifications (
    notification_uuid        uuid        NOT NULL,
    type                     text        NOT NULL,
    subtype                  text,
    original_transaction_id  text,
    signed_date              timestamptz NOT NULL,
    environment              text        NOT NULL,
    payload                  jsonb       NOT NULL,
    status                   text        NOT NULL DEFAULT 'received',
    attempts                 smallint    NOT NULL DEFAULT 0,
    last_error               text,
    received_at              timestamptz DEFAULT now() NOT NULL,
    processed_at             timestamptz,
    CONSTRAINT pk_t_apple_notifications           PRIMARY KEY (notification_uuid),
    CONSTRAINT ck_t_apple_notifications_status    CHECK (status IN ('received','processed','ignored','failed','dead')),
    CONSTRAINT ck_t_apple_notifications_processed CHECK ((status IN ('processed','ignored')) = (processed_at IS NOT NULL)),
    CONSTRAINT ck_t_apple_notifications_attempts  CHECK (attempts >= 0)
);
CREATE INDEX ix_t_apple_notifications_pending ON t_apple_notifications (notification_uuid)
    WHERE status IN ('received','failed');
CREATE INDEX ix_t_apple_notifications_txn      ON t_apple_notifications (original_transaction_id);

-- ---------------------------------------------------------------------------
-- Alerts
-- ---------------------------------------------------------------------------

CREATE TABLE t_alert_rules (
    id          uuid        DEFAULT uuidv7() NOT NULL,
    device_id   uuid        NOT NULL,
    spot_id     uuid,
    h3_cell     bigint,
    radius_m    integer,
    min_score   smallint    NOT NULL,
    days_mask   smallint    NOT NULL,
    start_local time        NOT NULL,
    end_local   time        NOT NULL,
    tz          text        NOT NULL,
    enabled     boolean     DEFAULT true NOT NULL,
    CONSTRAINT pk_t_alert_rules              PRIMARY KEY (id),
    CONSTRAINT fk_t_alert_rules_t_devices     FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_alert_rules_t_spots       FOREIGN KEY (spot_id)   REFERENCES t_spots (id),
    CONSTRAINT ck_t_alert_rules_target        CHECK (spot_id IS NOT NULL OR h3_cell IS NOT NULL),
    CONSTRAINT ck_t_alert_rules_geo_pair      CHECK ((h3_cell IS NULL) = (radius_m IS NULL)),
    CONSTRAINT ck_t_alert_rules_min_score     CHECK (min_score BETWEEN 0 AND 100),
    CONSTRAINT ck_t_alert_rules_days_mask     CHECK (days_mask BETWEEN 0 AND 127),
    CONSTRAINT ck_t_alert_rules_window        CHECK (start_local < end_local)
);
CREATE INDEX ix_t_alert_rules_device_id ON t_alert_rules (device_id);
CREATE INDEX ix_t_alert_rules_spot_enabled ON t_alert_rules (spot_id) WHERE enabled;

CREATE TABLE t_alert_deliveries (
    rule_id       uuid        NOT NULL,
    window_start  timestamptz NOT NULL,
    status        text        NOT NULL DEFAULT 'claimed',
    attempts      smallint    NOT NULL DEFAULT 0,
    claimed_at    timestamptz DEFAULT now() NOT NULL,
    sent_at       timestamptz,
    apns_status   integer,
    last_error    text,
    CONSTRAINT pk_t_alert_deliveries            PRIMARY KEY (rule_id, window_start),
    CONSTRAINT fk_t_alert_deliveries_t_alert_rules FOREIGN KEY (rule_id) REFERENCES t_alert_rules (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_alert_deliveries_status     CHECK (status IN ('claimed','sent','failed','dead')),
    CONSTRAINT ck_t_alert_deliveries_sent_pair  CHECK ((status = 'sent') = (sent_at IS NOT NULL)),
    CONSTRAINT ck_t_alert_deliveries_attempts   CHECK (attempts >= 0)
);
CREATE INDEX ix_t_alert_deliveries_pending ON t_alert_deliveries (rule_id, window_start)
    WHERE status IN ('claimed','failed');
CREATE INDEX ix_t_alert_deliveries_window  ON t_alert_deliveries (window_start);

-- ---------------------------------------------------------------------------
-- API keys (machine credentials for MCP/agents)
-- ---------------------------------------------------------------------------

CREATE TABLE t_api_keys (
    id             uuid        DEFAULT uuidv7() NOT NULL,
    name           text        NOT NULL,
    description    text        DEFAULT '' NOT NULL,
    key_prefix     text        NOT NULL,
    key_hash       bytea       NOT NULL,
    rate_limit_rps integer     DEFAULT 60 NOT NULL,
    expires_at     timestamptz,
    last_used_at   timestamptz,
    revoked_at     timestamptz,
    created_at     timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_api_keys              PRIMARY KEY (id),
    CONSTRAINT uq_t_api_keys_key_hash     UNIQUE (key_hash),
    CONSTRAINT uq_t_api_keys_key_prefix   UNIQUE (key_prefix),
    CONSTRAINT ck_t_api_keys_rate_limit   CHECK (rate_limit_rps > 0),
    CONSTRAINT ck_t_api_keys_key_hash_len CHECK (octet_length(key_hash) = 32)
);

CREATE TABLE t_api_key_grants (
    id          uuid        DEFAULT uuidv7() NOT NULL,
    api_key_id  uuid        NOT NULL,
    effect      text        NOT NULL,
    resource    text        NOT NULL,
    action      text        NOT NULL,
    created_at  timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_api_key_grants              PRIMARY KEY (id),
    CONSTRAINT uq_t_api_key_grants_grant        UNIQUE (api_key_id, effect, resource, action),
    CONSTRAINT fk_t_api_key_grants_t_api_keys   FOREIGN KEY (api_key_id) REFERENCES t_api_keys (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_api_key_grants_effect       CHECK (effect IN ('allow','deny'))
);
-- Lookups by api_key_id are served by uq_t_api_key_grants_grant (leftmost).

-- ---------------------------------------------------------------------------
-- Scoring / weather / crowd
-- ---------------------------------------------------------------------------

CREATE TABLE t_scoring_configs (
    version      smallint    NOT NULL,
    weights      jsonb       NOT NULL,
    thresholds   jsonb       NOT NULL,
    active_from  timestamptz NOT NULL,
    CONSTRAINT pk_t_scoring_configs PRIMARY KEY (version)
);

CREATE TABLE t_spot_scores (
    spot_id       uuid        NOT NULL,
    ts_hour       timestamptz NOT NULL,
    config_version smallint   NOT NULL,
    score         smallint    NOT NULL,
    s_weather     smallint,
    s_crowd       smallint,
    s_amenity     smallint,
    s_shade       smallint,
    s_fresh       smallint,
    cap_reason    text,
    reasons       jsonb       NOT NULL,
    confidence    smallint    NOT NULL,
    computed_at   timestamptz NOT NULL,
    CONSTRAINT pk_t_spot_scores               PRIMARY KEY (spot_id, ts_hour),
    CONSTRAINT fk_t_spot_scores_t_spots       FOREIGN KEY (spot_id) REFERENCES t_spots (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_spot_scores_t_scoring_configs FOREIGN KEY (config_version) REFERENCES t_scoring_configs (version),
    CONSTRAINT ck_t_spot_scores_score         CHECK (score BETWEEN 0 AND 100),
    CONSTRAINT ck_t_spot_scores_components    CHECK (
        (s_weather  IS NULL OR s_weather  BETWEEN 0 AND 100) AND
        (s_crowd    IS NULL OR s_crowd    BETWEEN 0 AND 100) AND
        (s_amenity  IS NULL OR s_amenity  BETWEEN 0 AND 100) AND
        (s_shade    IS NULL OR s_shade    BETWEEN 0 AND 100) AND
        (s_fresh    IS NULL OR s_fresh    BETWEEN 0 AND 100)),
    CONSTRAINT ck_t_spot_scores_confidence   CHECK (confidence BETWEEN 1 AND 3)
) WITH (fillfactor = 85);
-- Ranking index: score-ordered recommendation queries exist in code.
CREATE INDEX ix_t_spot_scores_hour_score ON t_spot_scores (ts_hour, score DESC) INCLUDE (spot_id);

CREATE TABLE t_weather_hourly_forecasts (
    cell_id     bigint      NOT NULL,
    ts          timestamptz NOT NULL,
    temp        real,
    feels_like  real,
    precip_prob smallint,
    wind        real,
    uv          real,
    cloud       smallint,
    provider    text        NOT NULL,
    fetched_at  timestamptz NOT NULL,
    CONSTRAINT pk_t_weather_hourly_forecasts PRIMARY KEY (cell_id, ts),
    CONSTRAINT ck_t_weather_hourly_precip     CHECK (precip_prob IS NULL OR precip_prob BETWEEN 0 AND 100),
    CONSTRAINT ck_t_weather_hourly_cloud      CHECK (cloud IS NULL OR cloud BETWEEN 0 AND 100)
);
CREATE INDEX ix_t_weather_hourly_forecasts_ts ON t_weather_hourly_forecasts (ts);

CREATE TABLE t_crowd_curves (
    kind            text     NOT NULL,
    dow             smallint NOT NULL,
    hour            smallint NOT NULL,
    factor          real     NOT NULL,
    config_version  smallint NOT NULL,
    CONSTRAINT pk_t_crowd_curves               PRIMARY KEY (kind, dow, hour, config_version),
    CONSTRAINT fk_t_crowd_curves_t_scoring_configs FOREIGN KEY (config_version) REFERENCES t_scoring_configs (version),
    CONSTRAINT ck_t_crowd_curves_dow           CHECK (dow BETWEEN 0 AND 6),
    CONSTRAINT ck_t_crowd_curves_hour          CHECK (hour BETWEEN 0 AND 23),
    CONSTRAINT ck_t_crowd_curves_factor        CHECK (factor >= 0)
);

CREATE TABLE t_crowd_estimates (
    spot_id    uuid        NOT NULL,
    dow        smallint    NOT NULL,
    hour       smallint    NOT NULL,
    level      smallint    NOT NULL,
    source     text        NOT NULL,
    n_reports  integer     DEFAULT 0 NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT pk_t_crowd_estimates           PRIMARY KEY (spot_id, dow, hour),
    CONSTRAINT fk_t_crowd_estimates_t_spots   FOREIGN KEY (spot_id) REFERENCES t_spots (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_crowd_estimates_source    CHECK (source IN ('model','community','provider')),
    CONSTRAINT ck_t_crowd_estimates_level     CHECK (level BETWEEN 0 AND 100),
    CONSTRAINT ck_t_crowd_estimates_dow       CHECK (dow BETWEEN 0 AND 6),
    CONSTRAINT ck_t_crowd_estimates_hour      CHECK (hour BETWEEN 0 AND 23),
    CONSTRAINT ck_t_crowd_estimates_n_reports CHECK (n_reports >= 0)
);

CREATE TABLE t_crowd_reports (
    id          uuid        DEFAULT uuidv7() NOT NULL,
    spot_id     uuid        NOT NULL,
    device_id   uuid        NOT NULL,
    level       smallint    NOT NULL,
    local_dow   smallint    NOT NULL,
    local_hour  smallint    NOT NULL,
    created_day date        GENERATED ALWAYS AS ((created_at AT TIME ZONE 'UTC')::date) STORED,
    created_at  timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_crowd_reports                    PRIMARY KEY (id),
    CONSTRAINT uq_t_crowd_reports_slot               UNIQUE (device_id, spot_id, created_day, local_hour),
    CONSTRAINT fk_t_crowd_reports_t_spots            FOREIGN KEY (spot_id) REFERENCES t_spots (id),
    CONSTRAINT fk_t_crowd_reports_t_devices          FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_crowd_reports_level              CHECK (level BETWEEN 1 AND 3),
    CONSTRAINT ck_t_crowd_reports_local_dow          CHECK (local_dow BETWEEN 0 AND 6),
    CONSTRAINT ck_t_crowd_reports_local_hour         CHECK (local_hour BETWEEN 0 AND 23)
);
CREATE INDEX ix_t_crowd_reports_spot_created ON t_crowd_reports (spot_id, created_at);
CREATE INDEX ix_t_crowd_reports_device       ON t_crowd_reports (device_id);

-- ---------------------------------------------------------------------------
-- Reports / feedback / events
-- ---------------------------------------------------------------------------

CREATE TABLE t_spot_reports (
    id          uuid        DEFAULT uuidv7() NOT NULL,
    spot_id     uuid        NOT NULL,
    device_id   uuid        NOT NULL,
    type        text        NOT NULL,
    issue_code  text,
    status      text        NOT NULL DEFAULT 'pending',
    reviewed_at timestamptz,
    created_day date        DEFAULT ((now() AT TIME ZONE 'UTC')::date) NOT NULL,
    created_at  timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_spot_reports               PRIMARY KEY (id),
    CONSTRAINT uq_t_spot_reports_dedupe        UNIQUE (device_id, spot_id, type, created_day),
    CONSTRAINT fk_t_spot_reports_t_spots       FOREIGN KEY (spot_id) REFERENCES t_spots (id),
    CONSTRAINT fk_t_spot_reports_t_devices     FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_spot_reports_type          CHECK (type IN ('confirm','missing','closed','issue')),
    CONSTRAINT ck_t_spot_reports_status        CHECK (status IN ('pending','accepted','rejected','duplicate')),
    CONSTRAINT ck_t_spot_reports_issue_pair    CHECK ((type = 'issue') = (issue_code IS NOT NULL)),
    CONSTRAINT ck_t_spot_reports_reviewed_pair CHECK ((status = 'pending') = (reviewed_at IS NULL))
);
CREATE INDEX ix_t_spot_reports_spot_id ON t_spot_reports (spot_id);

CREATE TABLE t_score_feedbacks (
    id         uuid        DEFAULT uuidv7() NOT NULL,
    spot_id    uuid        NOT NULL,
    ts_hour    timestamptz NOT NULL,
    device_id  uuid        NOT NULL,
    verdict    smallint    NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_score_feedbacks             PRIMARY KEY (id),
    CONSTRAINT uq_t_score_feedbacks_slot        UNIQUE (device_id, spot_id, ts_hour),
    CONSTRAINT fk_t_score_feedbacks_t_spots     FOREIGN KEY (spot_id) REFERENCES t_spots (id),
    CONSTRAINT fk_t_score_feedbacks_t_devices   FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_score_feedbacks_verdict     CHECK (verdict BETWEEN -1 AND 1)
);

CREATE TABLE t_events (
    id         uuid        DEFAULT uuidv7() NOT NULL,
    device_id  uuid        NOT NULL,
    name       text        NOT NULL,
    props      jsonb       DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_events               PRIMARY KEY (id),
    CONSTRAINT fk_t_events_t_devices     FOREIGN KEY (device_id) REFERENCES t_devices (id) ON DELETE CASCADE
);
CREATE INDEX ix_t_events_device_created ON t_events (device_id, created_at);
CREATE INDEX ix_t_events_name_created   ON t_events (name, created_at);
CREATE INDEX ix_t_events_created_brin   ON t_events USING brin (created_at);

-- ---------------------------------------------------------------------------
-- Jobs / quota / plans / holidays
-- ---------------------------------------------------------------------------

CREATE TABLE t_job_runs (
    id             bigint GENERATED ALWAYS AS IDENTITY,
    job            text        NOT NULL,
    city_pack_id   uuid,
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz,
    status         text        NOT NULL,
    rows_affected  integer,
    error          text,
    CONSTRAINT pk_t_job_runs                  PRIMARY KEY (id),
    CONSTRAINT fk_t_job_runs_t_city_packs     FOREIGN KEY (city_pack_id) REFERENCES t_city_packs (id),
    CONSTRAINT ck_t_job_runs_status           CHECK (status IN ('running','succeeded','failed','abandoned')),
    CONSTRAINT ck_t_job_runs_finished_pair    CHECK ((status = 'running') = (finished_at IS NULL))
);
CREATE INDEX ix_t_job_runs_job_started ON t_job_runs (job, started_at DESC);

CREATE TABLE t_worker_job_schedules (
    job              text        NOT NULL,
    interval_seconds integer     NOT NULL,
    enabled          boolean     NOT NULL DEFAULT true,
    offset_seconds   integer     NOT NULL DEFAULT 0,
    updated_at       timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_worker_job_schedules          PRIMARY KEY (job),
    CONSTRAINT ck_t_worker_job_schedules_interval CHECK (interval_seconds BETWEEN 60 AND 2592000),
    CONSTRAINT ck_t_worker_job_schedules_offset   CHECK (offset_seconds >= 0 AND offset_seconds < interval_seconds)
);

-- `day` is the UTC calendar date; quota resets at 00:00 UTC.
CREATE TABLE t_provider_quotas (
    provider   text        NOT NULL,
    day        date        NOT NULL,
    used       integer     DEFAULT 0 NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_provider_quotas PRIMARY KEY (provider, day),
    CONSTRAINT ck_t_provider_quotas_used  CHECK (used >= 0)
);

CREATE TABLE t_plan_limits (
    plan        text    NOT NULL,
    feature     text    NOT NULL,
    limit_value integer,
    CONSTRAINT pk_t_plan_limits     PRIMARY KEY (plan, feature),
    CONSTRAINT ck_t_plan_limits_plan CHECK (plan IN ('free','plus'))
);

CREATE TABLE t_holidays (
    country text        NOT NULL,
    region  text        DEFAULT '' NOT NULL,
    date    date        NOT NULL,
    kind    text        NOT NULL,
    name    text,
    CONSTRAINT pk_t_holidays      PRIMARY KEY (country, region, date, kind),
    CONSTRAINT ck_t_holidays_country CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT ck_t_holidays_kind    CHECK (kind IN ('public','school'))
);

-- ---------------------------------------------------------------------------
-- State transition audit log (written by strict-machine triggers)
-- ---------------------------------------------------------------------------

CREATE TABLE t_state_transitions (
    id          bigint      GENERATED ALWAYS AS IDENTITY,
    entity      text        NOT NULL,
    entity_id   text        NOT NULL,
    from_status text,
    to_status   text        NOT NULL,
    changed_at  timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_state_transitions PRIMARY KEY (id)
);
CREATE INDEX ix_t_state_transitions_entity ON t_state_transitions (entity, entity_id, changed_at);

-- ===========================================================================
-- Strict state machines — triggers reject illegal transitions (23514)
-- and log accepted ones into t_state_transitions.
-- ===========================================================================

-- fn_log_transition appends a row to t_state_transitions. Called by strict
-- machine triggers after a transition is accepted.
CREATE FUNCTION fn_log_transition(p_entity text, p_entity_id text,
                                  p_from text, p_to text)
RETURNS void LANGUAGE sql AS $$
    INSERT INTO t_state_transitions (entity, entity_id, from_status, to_status)
    VALUES (p_entity, p_entity_id, p_from, p_to);
$$;

-- t_spots: pending→active|hidden; active→closed|hidden|merged;
--          closed→active|hidden|merged; hidden→active|merged; merged terminal.
CREATE FUNCTION fn_t_spots_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'pending' THEN ARRAY['active','hidden']
        WHEN 'active'  THEN ARRAY['closed','hidden','merged']
        WHEN 'closed'  THEN ARRAY['active','hidden','merged']
        WHEN 'hidden'  THEN ARRAY['active','merged']
        ELSE ARRAY[]::text[]
    END;
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(allowed, OLD.status, NEW.status);
        PERFORM fn_log_transition('spot', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_spots_status
    BEFORE UPDATE OF status ON t_spots
    FOR EACH ROW EXECUTE FUNCTION fn_t_spots_transition();

-- t_spot_reports: pending→accepted|rejected|duplicate; rest terminal.
CREATE FUNCTION fn_t_spot_reports_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(
            CASE WHEN OLD.status = 'pending'
                 THEN ARRAY['accepted','rejected','duplicate']
                 ELSE ARRAY[]::text[] END,
            OLD.status, NEW.status);
        PERFORM fn_log_transition('spot_report', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_spot_reports_status
    BEFORE UPDATE OF status ON t_spot_reports
    FOR EACH ROW EXECUTE FUNCTION fn_t_spot_reports_transition();

-- t_city_packs: draft→beta|retired; beta→live|draft|retired;
--               live→beta|retired; retired terminal.
CREATE FUNCTION fn_t_city_packs_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'draft'   THEN ARRAY['beta','retired']
        WHEN 'beta'    THEN ARRAY['live','draft','retired']
        WHEN 'live'    THEN ARRAY['beta','retired']
        ELSE ARRAY[]::text[]
    END;
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(allowed, OLD.status, NEW.status);
        PERFORM fn_log_transition('city_pack', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_city_packs_status
    BEFORE UPDATE OF status ON t_city_packs
    FOR EACH ROW EXECUTE FUNCTION fn_t_city_packs_transition();

-- t_alert_deliveries: claimed→sent|failed; failed→claimed|dead;
--                     sent, dead terminal.
CREATE FUNCTION fn_t_alert_deliveries_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'claimed' THEN ARRAY['sent','failed']
        WHEN 'failed'  THEN ARRAY['claimed','dead']
        ELSE ARRAY[]::text[]
    END;
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(allowed, OLD.status, NEW.status);
        PERFORM fn_log_transition('alert_delivery', NEW.rule_id::text || '@' || NEW.window_start::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_alert_deliveries_status
    BEFORE UPDATE OF status ON t_alert_deliveries
    FOR EACH ROW EXECUTE FUNCTION fn_t_alert_deliveries_transition();

-- t_job_runs: running→succeeded|failed|abandoned; rest terminal.
CREATE FUNCTION fn_t_job_runs_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(
            CASE WHEN OLD.status = 'running'
                 THEN ARRAY['succeeded','failed','abandoned']
                 ELSE ARRAY[]::text[] END,
            OLD.status, NEW.status);
        PERFORM fn_log_transition('job_run', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_job_runs_status
    BEFORE UPDATE OF status ON t_job_runs
    FOR EACH ROW EXECUTE FUNCTION fn_t_job_runs_transition();

-- t_apple_notifications: received→processed|ignored|failed;
--                        failed→processed|ignored|dead; processed|ignored|dead terminal.
CREATE FUNCTION fn_t_apple_notifications_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'received' THEN ARRAY['processed','ignored','failed']
        WHEN 'failed'   THEN ARRAY['processed','ignored','dead']
        ELSE ARRAY[]::text[]
    END;
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(allowed, OLD.status, NEW.status);
        PERFORM fn_log_transition('apple_notification', NEW.notification_uuid::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_t_apple_notifications_status
    BEFORE UPDATE OF status ON t_apple_notifications
    FOR EACH ROW EXECUTE FUNCTION fn_t_apple_notifications_transition();
