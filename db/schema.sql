-- ============================================================================
-- Pickhour — PostgreSQL schema snapshot (PostgreSQL 18 + PostGIS 3.6)
-- Generated via pg_dump --schema-only from the dev database (migrations 1-13).
--
-- PURPOSE: review snapshot for consolidation. The schema is NOT final —
-- migrations will be rewritten/merged later. Review goals:
--   1. Tables that should be merged or are redundant
--   2. Missing columns/constraints/indexes worth adding
--   3. Anything over-engineered for current scale
--
-- Module map (backend/internal/modules):
--   spots        → spot, spot_amenity, spot_source, city_pack
--   ingest/osm   → spot_source, spot (pending → active flow)
--   weather      → weather_hourly (H3 cell res 6), provider_quota
--   crowd        → crowd_curve, crowd_estimate, crowd_report
--   scoring      → scoring_config, spot_score (partitioned by ts_hour)
--   recommendations → (reads spot_score/crowd_estimate, no own tables)
--   reports      → spot_report, score_feedback
--   devices      → device, device_subscription, apple_notification
--   alerts       → alert_rule, alert_delivery
--   entitlement  → subscription, plan_limit
--   events       → event (partitioned by created_at)
--   admin        → job_run, worker_job_schedule, scoring_config
--   apikeys      → api_key, api_key_grant
--   holidays     → holiday
-- ============================================================================

--
-- PostgreSQL database dump
--

\restrict rgkFcWSdtGu78ZcbLMh0bfFeKrqvaQ1sQC352dOTrOYhcI1XX3rrTJMpPynfGTb

-- Dumped from database version 18.6 (Debian 18.6-1.pgdg13+2)
-- Dumped by pg_dump version 18.6 (Debian 18.6-1.pgdg13+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA public;


--
-- Name: SCHEMA public; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON SCHEMA public IS 'standard public schema';


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: alert_delivery; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_delivery (
    rule_id uuid NOT NULL,
    window_start timestamp with time zone NOT NULL,
    sent_at timestamp with time zone,
    apns_status integer
);


--
-- Name: alert_rule; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_rule (
    id uuid DEFAULT uuidv7() NOT NULL,
    device_id uuid NOT NULL,
    spot_id uuid,
    h3_cell bigint,
    radius_m integer,
    min_score smallint NOT NULL,
    days_mask smallint NOT NULL,
    start_local time without time zone NOT NULL,
    end_local time without time zone NOT NULL,
    tz text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    CONSTRAINT alert_rule_check CHECK (((spot_id IS NOT NULL) OR (h3_cell IS NOT NULL)))
);


--
-- Name: api_key; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_key (
    id uuid DEFAULT uuidv7() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL,
    rate_limit_rps integer DEFAULT 60 NOT NULL,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: api_key_grant; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_key_grant (
    id uuid DEFAULT uuidv7() NOT NULL,
    api_key_id uuid NOT NULL,
    effect text NOT NULL,
    resource text NOT NULL,
    action text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT api_key_grant_effect_check CHECK ((effect = ANY (ARRAY['allow'::text, 'deny'::text])))
);


--
-- Name: apple_notification; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apple_notification (
    notification_uuid uuid NOT NULL,
    type text NOT NULL,
    subtype text,
    original_transaction_id text,
    signed_date timestamp with time zone NOT NULL,
    environment text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone
);


--
-- Name: city_pack; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.city_pack (
    id uuid DEFAULT uuidv7() NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    country character(2) NOT NULL,
    tz text NOT NULL,
    boundary public.geometry(MultiPolygon,4326) NOT NULL,
    status text NOT NULL,
    osm_extract text,
    last_import_at timestamp with time zone,
    CONSTRAINT city_pack_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'beta'::text, 'live'::text, 'retired'::text])))
);


--
-- Name: crowd_curve; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.crowd_curve (
    kind text NOT NULL,
    dow smallint NOT NULL,
    hour smallint NOT NULL,
    factor real NOT NULL,
    config_version smallint NOT NULL
);


--
-- Name: crowd_estimate; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.crowd_estimate (
    spot_id uuid NOT NULL,
    dow smallint NOT NULL,
    hour smallint NOT NULL,
    level smallint NOT NULL,
    source text NOT NULL,
    n_reports integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT crowd_estimate_source_check CHECK ((source = ANY (ARRAY['model'::text, 'community'::text, 'provider'::text])))
);


--
-- Name: crowd_report; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.crowd_report (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    device_id uuid NOT NULL,
    level smallint NOT NULL,
    local_dow smallint NOT NULL,
    local_hour smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT crowd_report_level_check CHECK (((level >= 1) AND (level <= 3)))
);


--
-- Name: device; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device (
    id uuid DEFAULT uuidv7() NOT NULL,
    apns_token text,
    apns_env text,
    locale text,
    tz text,
    app_version text,
    prefs jsonb DEFAULT '{}'::jsonb NOT NULL,
    attest_key_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone,
    deleted_at timestamp with time zone
);


--
-- Name: device_subscription; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_subscription (
    device_id uuid NOT NULL,
    original_transaction_id text NOT NULL,
    linked_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event (
    id uuid DEFAULT uuidv7() NOT NULL,
    device_id uuid NOT NULL,
    name text NOT NULL,
    props jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
)
PARTITION BY RANGE (created_at);


--
-- Name: event_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_default (
    id uuid DEFAULT uuidv7() CONSTRAINT event_id_not_null NOT NULL,
    device_id uuid CONSTRAINT event_device_id_not_null NOT NULL,
    name text CONSTRAINT event_name_not_null NOT NULL,
    props jsonb DEFAULT '{}'::jsonb CONSTRAINT event_props_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() CONSTRAINT event_created_at_not_null NOT NULL
);


--
-- Name: holiday; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.holiday (
    country character(2) NOT NULL,
    region text DEFAULT ''::text NOT NULL,
    date date NOT NULL,
    kind text NOT NULL,
    name text,
    CONSTRAINT holiday_kind_check CHECK ((kind = ANY (ARRAY['public'::text, 'school'::text])))
);


--
-- Name: job_run; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.job_run (
    id bigint NOT NULL,
    job text NOT NULL,
    city_pack_id uuid,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    status text NOT NULL,
    rows_affected integer,
    error text
);


--
-- Name: job_run_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.job_run_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: job_run_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.job_run_id_seq OWNED BY public.job_run.id;


--
-- Name: plan_limit; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plan_limit (
    plan text NOT NULL,
    feature text NOT NULL,
    limit_value integer,
    CONSTRAINT plan_limit_plan_check CHECK ((plan = ANY (ARRAY['free'::text, 'plus'::text])))
);


--
-- Name: provider_quota; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.provider_quota (
    provider text NOT NULL,
    day date NOT NULL,
    used integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_quota_used_check CHECK ((used >= 0))
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    version bigint NOT NULL,
    dirty boolean NOT NULL
);


--
-- Name: score_feedback; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.score_feedback (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    ts_hour timestamp with time zone NOT NULL,
    device_id uuid NOT NULL,
    verdict smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: scoring_config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.scoring_config (
    version smallint NOT NULL,
    weights jsonb NOT NULL,
    thresholds jsonb NOT NULL,
    active_from timestamp with time zone NOT NULL
);


--
-- Name: spot; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot (
    id uuid DEFAULT uuidv7() NOT NULL,
    city_pack_id uuid NOT NULL,
    kind text NOT NULL,
    name text,
    names jsonb DEFAULT '{}'::jsonb NOT NULL,
    geom public.geography(Point,4326) NOT NULL,
    area public.geometry(MultiPolygon,4326),
    country character(2) NOT NULL,
    city text,
    tz text NOT NULL,
    cell_id bigint NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    merged_into uuid,
    last_verified_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT spot_kind_check CHECK ((kind = ANY (ARRAY['table'::text, 'site'::text, 'park'::text, 'beach'::text]))),
    CONSTRAINT spot_status_check CHECK ((status = ANY (ARRAY['active'::text, 'closed'::text, 'hidden'::text, 'merged'::text])))
);


--
-- Name: spot_amenity; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot_amenity (
    spot_id uuid NOT NULL,
    type text NOT NULL,
    distance_m smallint NOT NULL,
    source text NOT NULL
);


--
-- Name: spot_report; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot_report (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    device_id uuid NOT NULL,
    type text NOT NULL,
    issue_code text,
    status text DEFAULT 'pending'::text NOT NULL,
    created_day date DEFAULT CURRENT_DATE NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT spot_report_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'accepted'::text, 'rejected'::text, 'duplicate'::text]))),
    CONSTRAINT spot_report_type_check CHECK ((type = ANY (ARRAY['confirm'::text, 'missing'::text, 'closed'::text, 'issue'::text])))
);


--
-- Name: spot_score; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot_score (
    spot_id uuid NOT NULL,
    ts_hour timestamp with time zone NOT NULL,
    config_version smallint NOT NULL,
    score smallint NOT NULL,
    s_weather smallint,
    s_crowd smallint,
    s_amenity smallint,
    s_shade smallint,
    s_fresh smallint,
    cap_reason text,
    reasons jsonb NOT NULL,
    confidence smallint NOT NULL,
    computed_at timestamp with time zone NOT NULL,
    CONSTRAINT spot_score_confidence_check CHECK (((confidence >= 1) AND (confidence <= 3))),
    CONSTRAINT spot_score_score_check CHECK (((score >= 0) AND (score <= 100)))
)
PARTITION BY RANGE (ts_hour);


--
-- Name: spot_score_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot_score_default (
    spot_id uuid CONSTRAINT spot_score_spot_id_not_null NOT NULL,
    ts_hour timestamp with time zone CONSTRAINT spot_score_ts_hour_not_null NOT NULL,
    config_version smallint CONSTRAINT spot_score_config_version_not_null NOT NULL,
    score smallint CONSTRAINT spot_score_score_not_null NOT NULL,
    s_weather smallint,
    s_crowd smallint,
    s_amenity smallint,
    s_shade smallint,
    s_fresh smallint,
    cap_reason text,
    reasons jsonb CONSTRAINT spot_score_reasons_not_null NOT NULL,
    confidence smallint CONSTRAINT spot_score_confidence_not_null NOT NULL,
    computed_at timestamp with time zone CONSTRAINT spot_score_computed_at_not_null NOT NULL,
    CONSTRAINT spot_score_confidence_check CHECK (((confidence >= 1) AND (confidence <= 3))),
    CONSTRAINT spot_score_score_check CHECK (((score >= 0) AND (score <= 100)))
);


--
-- Name: spot_source; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.spot_source (
    spot_id uuid NOT NULL,
    source text NOT NULL,
    source_id text NOT NULL,
    version integer,
    imported_at timestamp with time zone NOT NULL
);


--
-- Name: subscription; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.subscription (
    original_transaction_id text NOT NULL,
    product_id text NOT NULL,
    status text NOT NULL,
    environment text NOT NULL,
    ownership text NOT NULL,
    is_trial boolean DEFAULT false NOT NULL,
    auto_renew boolean DEFAULT true NOT NULL,
    expires_at timestamp with time zone,
    grace_expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    last_signed_date timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT subscription_environment_check CHECK ((environment = ANY (ARRAY['production'::text, 'sandbox'::text]))),
    CONSTRAINT subscription_ownership_check CHECK ((ownership = ANY (ARRAY['purchased'::text, 'family_shared'::text]))),
    CONSTRAINT subscription_status_check CHECK ((status = ANY (ARRAY['active'::text, 'grace'::text, 'billing_retry'::text, 'expired'::text, 'revoked'::text, 'refunded'::text])))
);


--
-- Name: weather_hourly; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.weather_hourly (
    cell_id bigint NOT NULL,
    ts timestamp with time zone NOT NULL,
    temp real,
    feels_like real,
    precip_prob smallint,
    wind real,
    uv real,
    cloud smallint,
    provider text NOT NULL,
    fetched_at timestamp with time zone NOT NULL
)
PARTITION BY RANGE (ts);


--
-- Name: weather_hourly_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.weather_hourly_default (
    cell_id bigint CONSTRAINT weather_hourly_cell_id_not_null NOT NULL,
    ts timestamp with time zone CONSTRAINT weather_hourly_ts_not_null NOT NULL,
    temp real,
    feels_like real,
    precip_prob smallint,
    wind real,
    uv real,
    cloud smallint,
    provider text CONSTRAINT weather_hourly_provider_not_null NOT NULL,
    fetched_at timestamp with time zone CONSTRAINT weather_hourly_fetched_at_not_null NOT NULL
);


--
-- Name: worker_job_schedule; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.worker_job_schedule (
    job text NOT NULL,
    interval_seconds integer NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT worker_job_schedule_interval_seconds_check CHECK (((interval_seconds >= 60) AND (interval_seconds <= 2592000)))
);


--
-- Name: event_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event ATTACH PARTITION public.event_default DEFAULT;


--
-- Name: spot_score_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_score ATTACH PARTITION public.spot_score_default DEFAULT;


--
-- Name: weather_hourly_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.weather_hourly ATTACH PARTITION public.weather_hourly_default DEFAULT;


--
-- Name: job_run id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_run ALTER COLUMN id SET DEFAULT nextval('public.job_run_id_seq'::regclass);


--
-- Name: alert_delivery alert_delivery_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_delivery
    ADD CONSTRAINT alert_delivery_pkey PRIMARY KEY (rule_id, window_start);


--
-- Name: alert_rule alert_rule_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule
    ADD CONSTRAINT alert_rule_pkey PRIMARY KEY (id);


--
-- Name: api_key_grant api_key_grant_api_key_id_effect_resource_action_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key_grant
    ADD CONSTRAINT api_key_grant_api_key_id_effect_resource_action_key UNIQUE (api_key_id, effect, resource, action);


--
-- Name: api_key_grant api_key_grant_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key_grant
    ADD CONSTRAINT api_key_grant_pkey PRIMARY KEY (id);


--
-- Name: api_key api_key_key_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key
    ADD CONSTRAINT api_key_key_hash_key UNIQUE (key_hash);


--
-- Name: api_key api_key_key_prefix_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key
    ADD CONSTRAINT api_key_key_prefix_key UNIQUE (key_prefix);


--
-- Name: api_key api_key_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key
    ADD CONSTRAINT api_key_pkey PRIMARY KEY (id);


--
-- Name: apple_notification apple_notification_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apple_notification
    ADD CONSTRAINT apple_notification_pkey PRIMARY KEY (notification_uuid);


--
-- Name: city_pack city_pack_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.city_pack
    ADD CONSTRAINT city_pack_pkey PRIMARY KEY (id);


--
-- Name: city_pack city_pack_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.city_pack
    ADD CONSTRAINT city_pack_slug_key UNIQUE (slug);


--
-- Name: crowd_curve crowd_curve_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crowd_curve
    ADD CONSTRAINT crowd_curve_pkey PRIMARY KEY (kind, dow, hour, config_version);


--
-- Name: crowd_estimate crowd_estimate_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crowd_estimate
    ADD CONSTRAINT crowd_estimate_pkey PRIMARY KEY (spot_id, dow, hour);


--
-- Name: crowd_report crowd_report_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crowd_report
    ADD CONSTRAINT crowd_report_pkey PRIMARY KEY (id);


--
-- Name: device device_apns_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device
    ADD CONSTRAINT device_apns_token_key UNIQUE (apns_token);


--
-- Name: device device_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device
    ADD CONSTRAINT device_pkey PRIMARY KEY (id);


--
-- Name: device_subscription device_subscription_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_subscription
    ADD CONSTRAINT device_subscription_pkey PRIMARY KEY (device_id, original_transaction_id);


--
-- Name: event event_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event
    ADD CONSTRAINT event_pkey PRIMARY KEY (id, created_at);


--
-- Name: event_default event_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_default
    ADD CONSTRAINT event_default_pkey PRIMARY KEY (id, created_at);


--
-- Name: holiday holiday_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.holiday
    ADD CONSTRAINT holiday_pkey PRIMARY KEY (country, region, date, kind);


--
-- Name: job_run job_run_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.job_run
    ADD CONSTRAINT job_run_pkey PRIMARY KEY (id);


--
-- Name: plan_limit plan_limit_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plan_limit
    ADD CONSTRAINT plan_limit_pkey PRIMARY KEY (plan, feature);


--
-- Name: provider_quota provider_quota_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.provider_quota
    ADD CONSTRAINT provider_quota_pkey PRIMARY KEY (provider, day);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: score_feedback score_feedback_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.score_feedback
    ADD CONSTRAINT score_feedback_pkey PRIMARY KEY (id);


--
-- Name: scoring_config scoring_config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scoring_config
    ADD CONSTRAINT scoring_config_pkey PRIMARY KEY (version);


--
-- Name: spot_amenity spot_amenity_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_amenity
    ADD CONSTRAINT spot_amenity_pkey PRIMARY KEY (spot_id, type);


--
-- Name: spot spot_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot
    ADD CONSTRAINT spot_pkey PRIMARY KEY (id);


--
-- Name: spot_report spot_report_device_id_spot_id_type_created_day_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_report
    ADD CONSTRAINT spot_report_device_id_spot_id_type_created_day_key UNIQUE (device_id, spot_id, type, created_day);


--
-- Name: spot_report spot_report_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_report
    ADD CONSTRAINT spot_report_pkey PRIMARY KEY (id);


--
-- Name: spot_score spot_score_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_score
    ADD CONSTRAINT spot_score_pkey PRIMARY KEY (spot_id, ts_hour);


--
-- Name: spot_score_default spot_score_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_score_default
    ADD CONSTRAINT spot_score_default_pkey PRIMARY KEY (spot_id, ts_hour);


--
-- Name: spot_source spot_source_source_source_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_source
    ADD CONSTRAINT spot_source_source_source_id_key UNIQUE (source, source_id);


--
-- Name: subscription subscription_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.subscription
    ADD CONSTRAINT subscription_pkey PRIMARY KEY (original_transaction_id);


--
-- Name: weather_hourly weather_hourly_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.weather_hourly
    ADD CONSTRAINT weather_hourly_pkey PRIMARY KEY (cell_id, ts);


--
-- Name: weather_hourly_default weather_hourly_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.weather_hourly_default
    ADD CONSTRAINT weather_hourly_default_pkey PRIMARY KEY (cell_id, ts);


--
-- Name: worker_job_schedule worker_job_schedule_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.worker_job_schedule
    ADD CONSTRAINT worker_job_schedule_pkey PRIMARY KEY (job);


--
-- Name: api_key_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_key_active_idx ON public.api_key USING btree (created_at DESC) WHERE (revoked_at IS NULL);


--
-- Name: api_key_grant_key_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_key_grant_key_idx ON public.api_key_grant USING btree (api_key_id);


--
-- Name: event_device_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_device_day_idx ON ONLY public.event USING btree (device_id, created_at);


--
-- Name: event_default_device_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_default_device_id_created_at_idx ON public.event_default USING btree (device_id, created_at);


--
-- Name: event_name_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_name_day_idx ON ONLY public.event USING btree (name, created_at);


--
-- Name: event_default_name_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_default_name_created_at_idx ON public.event_default USING btree (name, created_at);


--
-- Name: spot_area_gist; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX spot_area_gist ON public.spot USING gist (area) WHERE (area IS NOT NULL);


--
-- Name: spot_cell_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX spot_cell_id_idx ON public.spot USING btree (cell_id);


--
-- Name: spot_city_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX spot_city_status_idx ON public.spot USING btree (city_pack_id, status);


--
-- Name: spot_geom_gist; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX spot_geom_gist ON public.spot USING gist (geom);


--
-- Name: event_default_device_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.event_device_day_idx ATTACH PARTITION public.event_default_device_id_created_at_idx;


--
-- Name: event_default_name_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.event_name_day_idx ATTACH PARTITION public.event_default_name_created_at_idx;


--
-- Name: event_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.event_pkey ATTACH PARTITION public.event_default_pkey;


--
-- Name: spot_score_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.spot_score_pkey ATTACH PARTITION public.spot_score_default_pkey;


--
-- Name: weather_hourly_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.weather_hourly_pkey ATTACH PARTITION public.weather_hourly_default_pkey;


--
-- Name: alert_delivery alert_delivery_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_delivery
    ADD CONSTRAINT alert_delivery_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.alert_rule(id) ON DELETE CASCADE;


--
-- Name: alert_rule alert_rule_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule
    ADD CONSTRAINT alert_rule_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: alert_rule alert_rule_spot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule
    ADD CONSTRAINT alert_rule_spot_id_fkey FOREIGN KEY (spot_id) REFERENCES public.spot(id);


--
-- Name: api_key_grant api_key_grant_api_key_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_key_grant
    ADD CONSTRAINT api_key_grant_api_key_id_fkey FOREIGN KEY (api_key_id) REFERENCES public.api_key(id) ON DELETE CASCADE;


--
-- Name: crowd_report crowd_report_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crowd_report
    ADD CONSTRAINT crowd_report_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: crowd_report crowd_report_spot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.crowd_report
    ADD CONSTRAINT crowd_report_spot_id_fkey FOREIGN KEY (spot_id) REFERENCES public.spot(id);


--
-- Name: device_subscription device_subscription_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_subscription
    ADD CONSTRAINT device_subscription_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: device_subscription device_subscription_original_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_subscription
    ADD CONSTRAINT device_subscription_original_transaction_id_fkey FOREIGN KEY (original_transaction_id) REFERENCES public.subscription(original_transaction_id);


--
-- Name: event event_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.event
    ADD CONSTRAINT event_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: score_feedback score_feedback_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.score_feedback
    ADD CONSTRAINT score_feedback_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: spot_amenity spot_amenity_spot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_amenity
    ADD CONSTRAINT spot_amenity_spot_id_fkey FOREIGN KEY (spot_id) REFERENCES public.spot(id) ON DELETE CASCADE;


--
-- Name: spot spot_city_pack_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot
    ADD CONSTRAINT spot_city_pack_id_fkey FOREIGN KEY (city_pack_id) REFERENCES public.city_pack(id);


--
-- Name: spot spot_merged_into_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot
    ADD CONSTRAINT spot_merged_into_fkey FOREIGN KEY (merged_into) REFERENCES public.spot(id);


--
-- Name: spot_report spot_report_device_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_report
    ADD CONSTRAINT spot_report_device_id_fkey FOREIGN KEY (device_id) REFERENCES public.device(id) ON DELETE CASCADE;


--
-- Name: spot_report spot_report_spot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_report
    ADD CONSTRAINT spot_report_spot_id_fkey FOREIGN KEY (spot_id) REFERENCES public.spot(id);


--
-- Name: spot_source spot_source_spot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.spot_source
    ADD CONSTRAINT spot_source_spot_id_fkey FOREIGN KEY (spot_id) REFERENCES public.spot(id);


--
-- PostgreSQL database dump complete
--

\unrestrict rgkFcWSdtGu78ZcbLMh0bfFeKrqvaQ1sQC352dOTrOYhcI1XX3rrTJMpPynfGTb

