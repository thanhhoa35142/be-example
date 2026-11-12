-- ============================================================================
-- Pickhour — PostgreSQL schema snapshot (PostgreSQL 18 + PostGIS 3.6)
-- Generated via pg_dump --schema-only from picnic_v2 (migrations 0001-0003).
--
-- Naming: all app tables are t_<plural>; constraints pk_/fk_/uq_/ck_/ix_.
-- Strict state machines are enforced by trg_* triggers (check_violation on
-- illegal transitions) and audited into t_state_transitions.
--
-- Module map (backend/internal/modules):
--   spots        → t_spots, t_spot_amenities, t_spot_sources, t_city_packs
--   ingest/osm   → t_spot_sources, t_spots (pending → active flow)
--   weather      → t_weather_hourly_forecasts (H3 cell res 6), t_provider_quotas
--   crowd        → t_crowd_curves, t_crowd_estimates, t_crowd_reports
--   scoring      → t_scoring_configs, t_spot_scores
--   recommendations → (reads t_spot_scores/t_crowd_estimates, no own tables)
--   reports      → t_spot_reports, t_score_feedbacks
--   devices      → t_devices, t_device_subscriptions, t_apple_notifications,
--                  t_subscriptions
--   alerts       → t_alert_rules, t_alert_deliveries
--   entitlement  → t_subscriptions, t_device_subscriptions, t_apple_notifications
--   notifier     → (APNs client; device push state on t_devices)
--   admin        → t_job_runs, t_worker_job_schedules
--   events       → t_events
--   apikeys      → t_api_keys, t_api_key_grants
--   holidays     → t_holidays (reference data for crowd model)
--   platform     → t_state_transitions, t_provider_quotas
-- ============================================================================

--
--
-- PostgreSQL database dump
--

\restrict qXPcLN7HfLvvFg1KMy6OUiQ8GOTNX9bby27KBNxN0e2N8ye0Dg22Be3WOLoJLJp

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
-- Name: postgis; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS postgis WITH SCHEMA public;


--
-- Name: EXTENSION postgis; Type: COMMENT; Schema: -; Owner: 
--

COMMENT ON EXTENSION postgis IS 'PostGIS geometry and geography spatial types and functions';


--
-- Name: fn_assert_transition(text[], text, text); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_assert_transition(p_allowed text[], p_from text, p_to text) RETURNS void
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_assert_transition(p_allowed text[], p_from text, p_to text) OWNER TO picnic;

--
-- Name: fn_log_transition(text, text, text, text); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_log_transition(p_entity text, p_entity_id text, p_from text, p_to text) RETURNS void
    LANGUAGE sql
    AS $$
    INSERT INTO t_state_transitions (entity, entity_id, from_status, to_status)
    VALUES (p_entity, p_entity_id, p_from, p_to);
$$;


ALTER FUNCTION public.fn_log_transition(p_entity text, p_entity_id text, p_from text, p_to text) OWNER TO picnic;

--
-- Name: fn_t_alert_deliveries_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_alert_deliveries_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_t_alert_deliveries_transition() OWNER TO picnic;

--
-- Name: fn_t_apple_notifications_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_apple_notifications_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_t_apple_notifications_transition() OWNER TO picnic;

--
-- Name: fn_t_city_packs_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_city_packs_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_t_city_packs_transition() OWNER TO picnic;

--
-- Name: fn_t_job_runs_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_job_runs_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_t_job_runs_transition() OWNER TO picnic;

--
-- Name: fn_t_spot_reports_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_spot_reports_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


ALTER FUNCTION public.fn_t_spot_reports_transition() OWNER TO picnic;

--
-- Name: fn_t_spots_transition(); Type: FUNCTION; Schema: public; Owner: picnic
--

CREATE FUNCTION public.fn_t_spots_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
        NEW.status_changed_at := now();
        PERFORM fn_log_transition('spot', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;


ALTER FUNCTION public.fn_t_spots_transition() OWNER TO picnic;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.schema_migrations (
    version bigint NOT NULL,
    dirty boolean NOT NULL
);


ALTER TABLE public.schema_migrations OWNER TO picnic;

--
-- Name: t_alert_deliveries; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_alert_deliveries (
    rule_id uuid NOT NULL,
    window_start timestamp with time zone NOT NULL,
    status text DEFAULT 'claimed'::text NOT NULL,
    attempts smallint DEFAULT 0 NOT NULL,
    claimed_at timestamp with time zone DEFAULT now() NOT NULL,
    sent_at timestamp with time zone,
    apns_status integer,
    last_error text,
    CONSTRAINT ck_t_alert_deliveries_attempts CHECK ((attempts >= 0)),
    CONSTRAINT ck_t_alert_deliveries_sent_pair CHECK (((status = 'sent'::text) = (sent_at IS NOT NULL))),
    CONSTRAINT ck_t_alert_deliveries_status CHECK ((status = ANY (ARRAY['claimed'::text, 'sent'::text, 'failed'::text, 'dead'::text])))
);


ALTER TABLE public.t_alert_deliveries OWNER TO picnic;

--
-- Name: t_alert_rules; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_alert_rules (
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
    CONSTRAINT ck_t_alert_rules_days_mask CHECK (((days_mask >= 0) AND (days_mask <= 127))),
    CONSTRAINT ck_t_alert_rules_geo_pair CHECK (((h3_cell IS NULL) = (radius_m IS NULL))),
    CONSTRAINT ck_t_alert_rules_min_score CHECK (((min_score >= 0) AND (min_score <= 100))),
    CONSTRAINT ck_t_alert_rules_target CHECK (((spot_id IS NOT NULL) OR (h3_cell IS NOT NULL))),
    CONSTRAINT ck_t_alert_rules_window CHECK ((start_local < end_local))
);


ALTER TABLE public.t_alert_rules OWNER TO picnic;

--
-- Name: t_api_key_grants; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_api_key_grants (
    id uuid DEFAULT uuidv7() NOT NULL,
    api_key_id uuid NOT NULL,
    effect text NOT NULL,
    resource text NOT NULL,
    action text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_api_key_grants_effect CHECK ((effect = ANY (ARRAY['allow'::text, 'deny'::text])))
);


ALTER TABLE public.t_api_key_grants OWNER TO picnic;

--
-- Name: t_api_keys; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_api_keys (
    id uuid DEFAULT uuidv7() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL,
    rate_limit_rps integer DEFAULT 60 NOT NULL,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_api_keys_key_hash_len CHECK ((octet_length(key_hash) = 32)),
    CONSTRAINT ck_t_api_keys_rate_limit CHECK ((rate_limit_rps > 0))
);


ALTER TABLE public.t_api_keys OWNER TO picnic;

--
-- Name: t_apple_notifications; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_apple_notifications (
    notification_uuid uuid NOT NULL,
    type text NOT NULL,
    subtype text,
    original_transaction_id text,
    signed_date timestamp with time zone NOT NULL,
    environment text NOT NULL,
    payload jsonb NOT NULL,
    status text DEFAULT 'received'::text NOT NULL,
    attempts smallint DEFAULT 0 NOT NULL,
    last_error text,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    CONSTRAINT ck_t_apple_notifications_attempts CHECK ((attempts >= 0)),
    CONSTRAINT ck_t_apple_notifications_processed CHECK (((status = ANY (ARRAY['processed'::text, 'ignored'::text])) = (processed_at IS NOT NULL))),
    CONSTRAINT ck_t_apple_notifications_status CHECK ((status = ANY (ARRAY['received'::text, 'processed'::text, 'ignored'::text, 'failed'::text, 'dead'::text])))
);


ALTER TABLE public.t_apple_notifications OWNER TO picnic;

--
-- Name: t_city_packs; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_city_packs (
    id uuid DEFAULT uuidv7() NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    country text NOT NULL,
    tz text NOT NULL,
    boundary public.geometry(MultiPolygon,4326) NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    osm_extract text,
    last_import_at timestamp with time zone,
    extract_fetched_at timestamp with time zone,
    CONSTRAINT ck_t_city_packs_country CHECK ((country ~ '^[A-Z]{2}$'::text)),
    CONSTRAINT ck_t_city_packs_live_needs_import CHECK (((status <> ALL (ARRAY['beta'::text, 'live'::text])) OR (last_import_at IS NOT NULL))),
    CONSTRAINT ck_t_city_packs_status CHECK ((status = ANY (ARRAY['draft'::text, 'beta'::text, 'live'::text, 'retired'::text])))
);


ALTER TABLE public.t_city_packs OWNER TO picnic;

--
-- Name: t_crowd_curves; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_crowd_curves (
    kind text NOT NULL,
    dow smallint NOT NULL,
    hour smallint NOT NULL,
    factor real NOT NULL,
    config_version smallint NOT NULL,
    CONSTRAINT ck_t_crowd_curves_dow CHECK (((dow >= 0) AND (dow <= 6))),
    CONSTRAINT ck_t_crowd_curves_factor CHECK ((factor >= (0)::double precision)),
    CONSTRAINT ck_t_crowd_curves_hour CHECK (((hour >= 0) AND (hour <= 23)))
);


ALTER TABLE public.t_crowd_curves OWNER TO picnic;

--
-- Name: t_crowd_estimates; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_crowd_estimates (
    spot_id uuid NOT NULL,
    dow smallint NOT NULL,
    hour smallint NOT NULL,
    level smallint NOT NULL,
    source text NOT NULL,
    n_reports integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_t_crowd_estimates_dow CHECK (((dow >= 0) AND (dow <= 6))),
    CONSTRAINT ck_t_crowd_estimates_hour CHECK (((hour >= 0) AND (hour <= 23))),
    CONSTRAINT ck_t_crowd_estimates_level CHECK (((level >= 0) AND (level <= 100))),
    CONSTRAINT ck_t_crowd_estimates_n_reports CHECK ((n_reports >= 0)),
    CONSTRAINT ck_t_crowd_estimates_source CHECK ((source = ANY (ARRAY['model'::text, 'community'::text, 'provider'::text])))
);


ALTER TABLE public.t_crowd_estimates OWNER TO picnic;

--
-- Name: t_crowd_reports; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_crowd_reports (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    device_id uuid NOT NULL,
    level smallint NOT NULL,
    local_dow smallint NOT NULL,
    local_hour smallint NOT NULL,
    created_day date GENERATED ALWAYS AS (((created_at AT TIME ZONE 'UTC'::text))::date) STORED,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_crowd_reports_level CHECK (((level >= 1) AND (level <= 3))),
    CONSTRAINT ck_t_crowd_reports_local_dow CHECK (((local_dow >= 0) AND (local_dow <= 6))),
    CONSTRAINT ck_t_crowd_reports_local_hour CHECK (((local_hour >= 0) AND (local_hour <= 23)))
);


ALTER TABLE public.t_crowd_reports OWNER TO picnic;

--
-- Name: t_device_subscriptions; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_device_subscriptions (
    device_id uuid NOT NULL,
    original_transaction_id text NOT NULL,
    linked_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE public.t_device_subscriptions OWNER TO picnic;

--
-- Name: t_devices; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_devices (
    id uuid DEFAULT uuidv7() NOT NULL,
    apns_token text,
    apns_env text,
    push_status text DEFAULT 'none'::text NOT NULL,
    push_status_changed_at timestamp with time zone,
    locale text,
    tz text,
    app_version text,
    prefs jsonb DEFAULT '{}'::jsonb NOT NULL,
    attest_key_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone,
    deleted_at timestamp with time zone,
    CONSTRAINT ck_t_devices_apns_env CHECK (((apns_env IS NULL) OR (apns_env = ANY (ARRAY['sandbox'::text, 'production'::text])))),
    CONSTRAINT ck_t_devices_push_status CHECK ((push_status = ANY (ARRAY['none'::text, 'active'::text, 'unregistered'::text]))),
    CONSTRAINT ck_t_devices_push_token_pair CHECK (((push_status = 'active'::text) = (apns_token IS NOT NULL)))
);


ALTER TABLE public.t_devices OWNER TO picnic;

--
-- Name: t_events; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_events (
    id uuid DEFAULT uuidv7() NOT NULL,
    device_id uuid NOT NULL,
    name text NOT NULL,
    props jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE public.t_events OWNER TO picnic;

--
-- Name: t_holidays; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_holidays (
    country text NOT NULL,
    region text DEFAULT ''::text NOT NULL,
    date date NOT NULL,
    kind text NOT NULL,
    name text,
    CONSTRAINT ck_t_holidays_country CHECK ((country ~ '^[A-Z]{2}$'::text)),
    CONSTRAINT ck_t_holidays_kind CHECK ((kind = ANY (ARRAY['public'::text, 'school'::text])))
);


ALTER TABLE public.t_holidays OWNER TO picnic;

--
-- Name: t_ingest_features; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_ingest_features (
    pack_id uuid NOT NULL,
    source text NOT NULL,
    source_id text NOT NULL,
    role text NOT NULL,
    kind text,
    amenity text,
    quality smallint,
    exclude_reason text,
    version integer,
    tags jsonb DEFAULT '{}'::jsonb NOT NULL,
    geom public.geometry(Geometry,4326),
    content_hash text NOT NULL,
    run_id bigint,
    seen_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_ingest_features_quality CHECK (((quality IS NULL) OR ((quality >= 0) AND (quality <= 100)))),
    CONSTRAINT ck_t_ingest_features_role CHECK ((role = ANY (ARRAY['candidate'::text, 'amenity'::text, 'excluded'::text])))
);


ALTER TABLE public.t_ingest_features OWNER TO picnic;

--
-- Name: t_job_runs; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_job_runs (
    id bigint NOT NULL,
    job text NOT NULL,
    city_pack_id uuid,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    status text NOT NULL,
    rows_affected integer,
    error text,
    stats jsonb,
    CONSTRAINT ck_t_job_runs_finished_pair CHECK (((status = 'running'::text) = (finished_at IS NULL))),
    CONSTRAINT ck_t_job_runs_status CHECK ((status = ANY (ARRAY['running'::text, 'succeeded'::text, 'failed'::text, 'abandoned'::text])))
);


ALTER TABLE public.t_job_runs OWNER TO picnic;

--
-- Name: t_job_runs_id_seq; Type: SEQUENCE; Schema: public; Owner: picnic
--

ALTER TABLE public.t_job_runs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.t_job_runs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: t_plan_limits; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_plan_limits (
    plan text NOT NULL,
    feature text NOT NULL,
    limit_value integer,
    CONSTRAINT ck_t_plan_limits_plan CHECK ((plan = ANY (ARRAY['free'::text, 'plus'::text])))
);


ALTER TABLE public.t_plan_limits OWNER TO picnic;

--
-- Name: t_provider_quotas; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_provider_quotas (
    provider text NOT NULL,
    day date NOT NULL,
    used integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_provider_quotas_used CHECK ((used >= 0))
);


ALTER TABLE public.t_provider_quotas OWNER TO picnic;

--
-- Name: t_score_feedbacks; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_score_feedbacks (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    ts_hour timestamp with time zone NOT NULL,
    device_id uuid NOT NULL,
    verdict smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_score_feedbacks_verdict CHECK (((verdict >= '-1'::integer) AND (verdict <= 1)))
);


ALTER TABLE public.t_score_feedbacks OWNER TO picnic;

--
-- Name: t_scoring_configs; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_scoring_configs (
    version smallint NOT NULL,
    weights jsonb NOT NULL,
    thresholds jsonb NOT NULL,
    active_from timestamp with time zone NOT NULL
);


ALTER TABLE public.t_scoring_configs OWNER TO picnic;

--
-- Name: t_spot_amenities; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_spot_amenities (
    spot_id uuid NOT NULL,
    type text NOT NULL,
    distance_m smallint NOT NULL,
    source text NOT NULL,
    count smallint DEFAULT 1 NOT NULL,
    CONSTRAINT ck_t_spot_amenities_count CHECK ((count >= 1))
);


ALTER TABLE public.t_spot_amenities OWNER TO picnic;

--
-- Name: t_spot_reports; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_spot_reports (
    id uuid DEFAULT uuidv7() NOT NULL,
    spot_id uuid NOT NULL,
    device_id uuid NOT NULL,
    type text NOT NULL,
    issue_code text,
    status text DEFAULT 'pending'::text NOT NULL,
    reviewed_at timestamp with time zone,
    created_day date DEFAULT ((now() AT TIME ZONE 'UTC'::text))::date NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_spot_reports_issue_pair CHECK (((type = 'issue'::text) = (issue_code IS NOT NULL))),
    CONSTRAINT ck_t_spot_reports_reviewed_pair CHECK (((status = 'pending'::text) = (reviewed_at IS NULL))),
    CONSTRAINT ck_t_spot_reports_status CHECK ((status = ANY (ARRAY['pending'::text, 'accepted'::text, 'rejected'::text, 'duplicate'::text]))),
    CONSTRAINT ck_t_spot_reports_type CHECK ((type = ANY (ARRAY['confirm'::text, 'missing'::text, 'closed'::text, 'issue'::text])))
);


ALTER TABLE public.t_spot_reports OWNER TO picnic;

--
-- Name: t_spot_scores; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_spot_scores (
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
    CONSTRAINT ck_t_spot_scores_components CHECK ((((s_weather IS NULL) OR ((s_weather >= 0) AND (s_weather <= 100))) AND ((s_crowd IS NULL) OR ((s_crowd >= 0) AND (s_crowd <= 100))) AND ((s_amenity IS NULL) OR ((s_amenity >= 0) AND (s_amenity <= 100))) AND ((s_shade IS NULL) OR ((s_shade >= 0) AND (s_shade <= 100))) AND ((s_fresh IS NULL) OR ((s_fresh >= 0) AND (s_fresh <= 100))))),
    CONSTRAINT ck_t_spot_scores_confidence CHECK (((confidence >= 1) AND (confidence <= 3))),
    CONSTRAINT ck_t_spot_scores_score CHECK (((score >= 0) AND (score <= 100)))
)
WITH (fillfactor='85');


ALTER TABLE public.t_spot_scores OWNER TO picnic;

--
-- Name: t_spot_sources; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_spot_sources (
    spot_id uuid NOT NULL,
    source text NOT NULL,
    source_id text NOT NULL,
    version integer,
    imported_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone,
    missing_runs smallint DEFAULT 0 NOT NULL,
    CONSTRAINT ck_t_spot_sources_missing_runs CHECK ((missing_runs >= 0)),
    CONSTRAINT ck_t_spot_sources_version CHECK (((version IS NULL) OR (version >= 0)))
);


ALTER TABLE public.t_spot_sources OWNER TO picnic;

--
-- Name: t_spots; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_spots (
    id uuid DEFAULT uuidv7() NOT NULL,
    city_pack_id uuid NOT NULL,
    kind text NOT NULL,
    name text,
    names jsonb DEFAULT '{}'::jsonb NOT NULL,
    geom public.geography(Point,4326) NOT NULL,
    area public.geometry(MultiPolygon,4326),
    country text NOT NULL,
    city text,
    tz text NOT NULL,
    cell_id bigint NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    merged_into uuid,
    last_verified_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    quality smallint,
    attrs jsonb DEFAULT '{}'::jsonb NOT NULL,
    curated boolean DEFAULT false NOT NULL,
    status_reason text,
    status_changed_at timestamp with time zone,
    CONSTRAINT ck_t_spots_country CHECK ((country ~ '^[A-Z]{2}$'::text)),
    CONSTRAINT ck_t_spots_kind CHECK ((kind = ANY (ARRAY['table'::text, 'site'::text, 'park'::text, 'beach'::text]))),
    CONSTRAINT ck_t_spots_merged_pair CHECK (((status = 'merged'::text) = (merged_into IS NOT NULL))),
    CONSTRAINT ck_t_spots_quality CHECK (((quality IS NULL) OR ((quality >= 0) AND (quality <= 100)))),
    CONSTRAINT ck_t_spots_status CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'closed'::text, 'hidden'::text, 'merged'::text])))
);


ALTER TABLE public.t_spots OWNER TO picnic;

--
-- Name: t_state_transitions; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_state_transitions (
    id bigint NOT NULL,
    entity text NOT NULL,
    entity_id text NOT NULL,
    from_status text,
    to_status text NOT NULL,
    changed_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE public.t_state_transitions OWNER TO picnic;

--
-- Name: t_state_transitions_id_seq; Type: SEQUENCE; Schema: public; Owner: picnic
--

ALTER TABLE public.t_state_transitions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.t_state_transitions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: t_subscriptions; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_subscriptions (
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
    CONSTRAINT ck_t_subscriptions_environment CHECK ((environment = ANY (ARRAY['production'::text, 'sandbox'::text]))),
    CONSTRAINT ck_t_subscriptions_grace_expiry CHECK (((status <> 'grace'::text) OR (grace_expires_at IS NOT NULL))),
    CONSTRAINT ck_t_subscriptions_ownership CHECK ((ownership = ANY (ARRAY['purchased'::text, 'family_shared'::text]))),
    CONSTRAINT ck_t_subscriptions_revoked_at CHECK (((status <> ALL (ARRAY['revoked'::text, 'refunded'::text])) OR (revoked_at IS NOT NULL))),
    CONSTRAINT ck_t_subscriptions_status CHECK ((status = ANY (ARRAY['active'::text, 'grace'::text, 'billing_retry'::text, 'expired'::text, 'revoked'::text, 'refunded'::text])))
);


ALTER TABLE public.t_subscriptions OWNER TO picnic;

--
-- Name: t_weather_hourly_forecasts; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_weather_hourly_forecasts (
    cell_id bigint NOT NULL,
    ts timestamp with time zone NOT NULL,
    temp real,
    feels_like real,
    precip_prob smallint,
    wind real,
    uv real,
    cloud smallint,
    provider text NOT NULL,
    fetched_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_t_weather_hourly_cloud CHECK (((cloud IS NULL) OR ((cloud >= 0) AND (cloud <= 100)))),
    CONSTRAINT ck_t_weather_hourly_precip CHECK (((precip_prob IS NULL) OR ((precip_prob >= 0) AND (precip_prob <= 100))))
);


ALTER TABLE public.t_weather_hourly_forecasts OWNER TO picnic;

--
-- Name: t_worker_job_schedules; Type: TABLE; Schema: public; Owner: picnic
--

CREATE TABLE public.t_worker_job_schedules (
    job text NOT NULL,
    interval_seconds integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    offset_seconds integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_t_worker_job_schedules_interval CHECK (((interval_seconds >= 60) AND (interval_seconds <= 2592000))),
    CONSTRAINT ck_t_worker_job_schedules_offset CHECK (((offset_seconds >= 0) AND (offset_seconds < interval_seconds)))
);


ALTER TABLE public.t_worker_job_schedules OWNER TO picnic;

--
-- Name: t_alert_deliveries pk_t_alert_deliveries; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_alert_deliveries
    ADD CONSTRAINT pk_t_alert_deliveries PRIMARY KEY (rule_id, window_start);


--
-- Name: t_alert_rules pk_t_alert_rules; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_alert_rules
    ADD CONSTRAINT pk_t_alert_rules PRIMARY KEY (id);


--
-- Name: t_api_key_grants pk_t_api_key_grants; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_key_grants
    ADD CONSTRAINT pk_t_api_key_grants PRIMARY KEY (id);


--
-- Name: t_api_keys pk_t_api_keys; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_keys
    ADD CONSTRAINT pk_t_api_keys PRIMARY KEY (id);


--
-- Name: t_apple_notifications pk_t_apple_notifications; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_apple_notifications
    ADD CONSTRAINT pk_t_apple_notifications PRIMARY KEY (notification_uuid);


--
-- Name: t_city_packs pk_t_city_packs; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_city_packs
    ADD CONSTRAINT pk_t_city_packs PRIMARY KEY (id);


--
-- Name: t_crowd_curves pk_t_crowd_curves; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_curves
    ADD CONSTRAINT pk_t_crowd_curves PRIMARY KEY (kind, dow, hour, config_version);


--
-- Name: t_crowd_estimates pk_t_crowd_estimates; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_estimates
    ADD CONSTRAINT pk_t_crowd_estimates PRIMARY KEY (spot_id, dow, hour);


--
-- Name: t_crowd_reports pk_t_crowd_reports; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_reports
    ADD CONSTRAINT pk_t_crowd_reports PRIMARY KEY (id);


--
-- Name: t_device_subscriptions pk_t_device_subscriptions; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_device_subscriptions
    ADD CONSTRAINT pk_t_device_subscriptions PRIMARY KEY (device_id, original_transaction_id);


--
-- Name: t_devices pk_t_devices; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_devices
    ADD CONSTRAINT pk_t_devices PRIMARY KEY (id);


--
-- Name: t_events pk_t_events; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_events
    ADD CONSTRAINT pk_t_events PRIMARY KEY (id);


--
-- Name: t_holidays pk_t_holidays; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_holidays
    ADD CONSTRAINT pk_t_holidays PRIMARY KEY (country, region, date, kind);


--
-- Name: t_ingest_features pk_t_ingest_features; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_ingest_features
    ADD CONSTRAINT pk_t_ingest_features PRIMARY KEY (pack_id, source, source_id);


--
-- Name: t_job_runs pk_t_job_runs; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_job_runs
    ADD CONSTRAINT pk_t_job_runs PRIMARY KEY (id);


--
-- Name: t_plan_limits pk_t_plan_limits; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_plan_limits
    ADD CONSTRAINT pk_t_plan_limits PRIMARY KEY (plan, feature);


--
-- Name: t_provider_quotas pk_t_provider_quotas; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_provider_quotas
    ADD CONSTRAINT pk_t_provider_quotas PRIMARY KEY (provider, day);


--
-- Name: t_score_feedbacks pk_t_score_feedbacks; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_score_feedbacks
    ADD CONSTRAINT pk_t_score_feedbacks PRIMARY KEY (id);


--
-- Name: t_scoring_configs pk_t_scoring_configs; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_scoring_configs
    ADD CONSTRAINT pk_t_scoring_configs PRIMARY KEY (version);


--
-- Name: t_spot_amenities pk_t_spot_amenities; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_amenities
    ADD CONSTRAINT pk_t_spot_amenities PRIMARY KEY (spot_id, type);


--
-- Name: t_spot_reports pk_t_spot_reports; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_reports
    ADD CONSTRAINT pk_t_spot_reports PRIMARY KEY (id);


--
-- Name: t_spot_scores pk_t_spot_scores; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_scores
    ADD CONSTRAINT pk_t_spot_scores PRIMARY KEY (spot_id, ts_hour);


--
-- Name: t_spot_sources pk_t_spot_sources; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_sources
    ADD CONSTRAINT pk_t_spot_sources PRIMARY KEY (source, source_id);


--
-- Name: t_spots pk_t_spots; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spots
    ADD CONSTRAINT pk_t_spots PRIMARY KEY (id);


--
-- Name: t_state_transitions pk_t_state_transitions; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_state_transitions
    ADD CONSTRAINT pk_t_state_transitions PRIMARY KEY (id);


--
-- Name: t_subscriptions pk_t_subscriptions; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_subscriptions
    ADD CONSTRAINT pk_t_subscriptions PRIMARY KEY (original_transaction_id);


--
-- Name: t_weather_hourly_forecasts pk_t_weather_hourly_forecasts; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_weather_hourly_forecasts
    ADD CONSTRAINT pk_t_weather_hourly_forecasts PRIMARY KEY (cell_id, ts);


--
-- Name: t_worker_job_schedules pk_t_worker_job_schedules; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_worker_job_schedules
    ADD CONSTRAINT pk_t_worker_job_schedules PRIMARY KEY (job);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: t_api_key_grants uq_t_api_key_grants_grant; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_key_grants
    ADD CONSTRAINT uq_t_api_key_grants_grant UNIQUE (api_key_id, effect, resource, action);


--
-- Name: t_api_keys uq_t_api_keys_key_hash; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_keys
    ADD CONSTRAINT uq_t_api_keys_key_hash UNIQUE (key_hash);


--
-- Name: t_api_keys uq_t_api_keys_key_prefix; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_keys
    ADD CONSTRAINT uq_t_api_keys_key_prefix UNIQUE (key_prefix);


--
-- Name: t_city_packs uq_t_city_packs_slug; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_city_packs
    ADD CONSTRAINT uq_t_city_packs_slug UNIQUE (slug);


--
-- Name: t_crowd_reports uq_t_crowd_reports_slot; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_reports
    ADD CONSTRAINT uq_t_crowd_reports_slot UNIQUE (device_id, spot_id, created_day, local_hour);


--
-- Name: t_score_feedbacks uq_t_score_feedbacks_slot; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_score_feedbacks
    ADD CONSTRAINT uq_t_score_feedbacks_slot UNIQUE (device_id, spot_id, ts_hour);


--
-- Name: t_spot_reports uq_t_spot_reports_dedupe; Type: CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_reports
    ADD CONSTRAINT uq_t_spot_reports_dedupe UNIQUE (device_id, spot_id, type, created_day);


--
-- Name: ix_t_alert_deliveries_pending; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_alert_deliveries_pending ON public.t_alert_deliveries USING btree (rule_id, window_start) WHERE (status = ANY (ARRAY['claimed'::text, 'failed'::text]));


--
-- Name: ix_t_alert_deliveries_window; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_alert_deliveries_window ON public.t_alert_deliveries USING btree (window_start);


--
-- Name: ix_t_alert_rules_device_id; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_alert_rules_device_id ON public.t_alert_rules USING btree (device_id);


--
-- Name: ix_t_alert_rules_spot_enabled; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_alert_rules_spot_enabled ON public.t_alert_rules USING btree (spot_id) WHERE enabled;


--
-- Name: ix_t_apple_notifications_pending; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_apple_notifications_pending ON public.t_apple_notifications USING btree (notification_uuid) WHERE (status = ANY (ARRAY['received'::text, 'failed'::text]));


--
-- Name: ix_t_apple_notifications_txn; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_apple_notifications_txn ON public.t_apple_notifications USING btree (original_transaction_id);


--
-- Name: ix_t_city_packs_boundary; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_city_packs_boundary ON public.t_city_packs USING gist (boundary);


--
-- Name: ix_t_crowd_reports_device; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_crowd_reports_device ON public.t_crowd_reports USING btree (device_id);


--
-- Name: ix_t_crowd_reports_spot_created; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_crowd_reports_spot_created ON public.t_crowd_reports USING btree (spot_id, created_at);


--
-- Name: ix_t_devices_push_active; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_devices_push_active ON public.t_devices USING btree (id) WHERE (push_status = 'active'::text);


--
-- Name: ix_t_events_created_brin; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_events_created_brin ON public.t_events USING brin (created_at);


--
-- Name: ix_t_events_device_created; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_events_device_created ON public.t_events USING btree (device_id, created_at);


--
-- Name: ix_t_events_name_created; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_events_name_created ON public.t_events USING btree (name, created_at);


--
-- Name: ix_t_ingest_features_geom; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_ingest_features_geom ON public.t_ingest_features USING gist (geom);


--
-- Name: ix_t_job_runs_job_started; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_job_runs_job_started ON public.t_job_runs USING btree (job, started_at DESC);


--
-- Name: ix_t_spot_reports_spot_id; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spot_reports_spot_id ON public.t_spot_reports USING btree (spot_id);


--
-- Name: ix_t_spot_scores_hour_score; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spot_scores_hour_score ON public.t_spot_scores USING btree (ts_hour, score DESC) INCLUDE (spot_id);


--
-- Name: ix_t_spot_sources_spot_id; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spot_sources_spot_id ON public.t_spot_sources USING btree (spot_id);


--
-- Name: ix_t_spots_area_gist; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spots_area_gist ON public.t_spots USING gist (area) WHERE (area IS NOT NULL);


--
-- Name: ix_t_spots_cell_id; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spots_cell_id ON public.t_spots USING btree (cell_id);


--
-- Name: ix_t_spots_city_pack_status; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spots_city_pack_status ON public.t_spots USING btree (city_pack_id, status);


--
-- Name: ix_t_spots_geom_gist; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_spots_geom_gist ON public.t_spots USING gist (geom);


--
-- Name: ix_t_state_transitions_entity; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_state_transitions_entity ON public.t_state_transitions USING btree (entity, entity_id, changed_at);


--
-- Name: ix_t_weather_hourly_forecasts_ts; Type: INDEX; Schema: public; Owner: picnic
--

CREATE INDEX ix_t_weather_hourly_forecasts_ts ON public.t_weather_hourly_forecasts USING btree (ts);


--
-- Name: uq_t_devices_apns_token; Type: INDEX; Schema: public; Owner: picnic
--

CREATE UNIQUE INDEX uq_t_devices_apns_token ON public.t_devices USING btree (apns_token) WHERE ((deleted_at IS NULL) AND (apns_token IS NOT NULL));


--
-- Name: t_alert_deliveries trg_t_alert_deliveries_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_alert_deliveries_status BEFORE UPDATE OF status ON public.t_alert_deliveries FOR EACH ROW EXECUTE FUNCTION public.fn_t_alert_deliveries_transition();


--
-- Name: t_apple_notifications trg_t_apple_notifications_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_apple_notifications_status BEFORE UPDATE OF status ON public.t_apple_notifications FOR EACH ROW EXECUTE FUNCTION public.fn_t_apple_notifications_transition();


--
-- Name: t_city_packs trg_t_city_packs_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_city_packs_status BEFORE UPDATE OF status ON public.t_city_packs FOR EACH ROW EXECUTE FUNCTION public.fn_t_city_packs_transition();


--
-- Name: t_job_runs trg_t_job_runs_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_job_runs_status BEFORE UPDATE OF status ON public.t_job_runs FOR EACH ROW EXECUTE FUNCTION public.fn_t_job_runs_transition();


--
-- Name: t_spot_reports trg_t_spot_reports_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_spot_reports_status BEFORE UPDATE OF status ON public.t_spot_reports FOR EACH ROW EXECUTE FUNCTION public.fn_t_spot_reports_transition();


--
-- Name: t_spots trg_t_spots_status; Type: TRIGGER; Schema: public; Owner: picnic
--

CREATE TRIGGER trg_t_spots_status BEFORE UPDATE OF status ON public.t_spots FOR EACH ROW EXECUTE FUNCTION public.fn_t_spots_transition();


--
-- Name: t_alert_deliveries fk_t_alert_deliveries_t_alert_rules; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_alert_deliveries
    ADD CONSTRAINT fk_t_alert_deliveries_t_alert_rules FOREIGN KEY (rule_id) REFERENCES public.t_alert_rules(id) ON DELETE CASCADE;


--
-- Name: t_alert_rules fk_t_alert_rules_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_alert_rules
    ADD CONSTRAINT fk_t_alert_rules_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_alert_rules fk_t_alert_rules_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_alert_rules
    ADD CONSTRAINT fk_t_alert_rules_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id);


--
-- Name: t_api_key_grants fk_t_api_key_grants_t_api_keys; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_api_key_grants
    ADD CONSTRAINT fk_t_api_key_grants_t_api_keys FOREIGN KEY (api_key_id) REFERENCES public.t_api_keys(id) ON DELETE CASCADE;


--
-- Name: t_crowd_curves fk_t_crowd_curves_t_scoring_configs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_curves
    ADD CONSTRAINT fk_t_crowd_curves_t_scoring_configs FOREIGN KEY (config_version) REFERENCES public.t_scoring_configs(version);


--
-- Name: t_crowd_estimates fk_t_crowd_estimates_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_estimates
    ADD CONSTRAINT fk_t_crowd_estimates_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id) ON DELETE CASCADE;


--
-- Name: t_crowd_reports fk_t_crowd_reports_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_reports
    ADD CONSTRAINT fk_t_crowd_reports_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_crowd_reports fk_t_crowd_reports_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_crowd_reports
    ADD CONSTRAINT fk_t_crowd_reports_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id);


--
-- Name: t_device_subscriptions fk_t_device_subscriptions_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_device_subscriptions
    ADD CONSTRAINT fk_t_device_subscriptions_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_device_subscriptions fk_t_device_subscriptions_t_subscriptions; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_device_subscriptions
    ADD CONSTRAINT fk_t_device_subscriptions_t_subscriptions FOREIGN KEY (original_transaction_id) REFERENCES public.t_subscriptions(original_transaction_id);


--
-- Name: t_events fk_t_events_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_events
    ADD CONSTRAINT fk_t_events_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_ingest_features fk_t_ingest_features_t_city_packs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_ingest_features
    ADD CONSTRAINT fk_t_ingest_features_t_city_packs FOREIGN KEY (pack_id) REFERENCES public.t_city_packs(id) ON DELETE CASCADE;


--
-- Name: t_ingest_features fk_t_ingest_features_t_job_runs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_ingest_features
    ADD CONSTRAINT fk_t_ingest_features_t_job_runs FOREIGN KEY (run_id) REFERENCES public.t_job_runs(id);


--
-- Name: t_job_runs fk_t_job_runs_t_city_packs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_job_runs
    ADD CONSTRAINT fk_t_job_runs_t_city_packs FOREIGN KEY (city_pack_id) REFERENCES public.t_city_packs(id);


--
-- Name: t_score_feedbacks fk_t_score_feedbacks_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_score_feedbacks
    ADD CONSTRAINT fk_t_score_feedbacks_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_score_feedbacks fk_t_score_feedbacks_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_score_feedbacks
    ADD CONSTRAINT fk_t_score_feedbacks_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id);


--
-- Name: t_spot_amenities fk_t_spot_amenities_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_amenities
    ADD CONSTRAINT fk_t_spot_amenities_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id) ON DELETE CASCADE;


--
-- Name: t_spot_reports fk_t_spot_reports_t_devices; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_reports
    ADD CONSTRAINT fk_t_spot_reports_t_devices FOREIGN KEY (device_id) REFERENCES public.t_devices(id) ON DELETE CASCADE;


--
-- Name: t_spot_reports fk_t_spot_reports_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_reports
    ADD CONSTRAINT fk_t_spot_reports_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id);


--
-- Name: t_spot_scores fk_t_spot_scores_t_scoring_configs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_scores
    ADD CONSTRAINT fk_t_spot_scores_t_scoring_configs FOREIGN KEY (config_version) REFERENCES public.t_scoring_configs(version);


--
-- Name: t_spot_scores fk_t_spot_scores_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_scores
    ADD CONSTRAINT fk_t_spot_scores_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id) ON DELETE CASCADE;


--
-- Name: t_spot_sources fk_t_spot_sources_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spot_sources
    ADD CONSTRAINT fk_t_spot_sources_t_spots FOREIGN KEY (spot_id) REFERENCES public.t_spots(id);


--
-- Name: t_spots fk_t_spots_t_city_packs; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spots
    ADD CONSTRAINT fk_t_spots_t_city_packs FOREIGN KEY (city_pack_id) REFERENCES public.t_city_packs(id);


--
-- Name: t_spots fk_t_spots_t_spots; Type: FK CONSTRAINT; Schema: public; Owner: picnic
--

ALTER TABLE ONLY public.t_spots
    ADD CONSTRAINT fk_t_spots_t_spots FOREIGN KEY (merged_into) REFERENCES public.t_spots(id);


--
-- PostgreSQL database dump complete
--

\unrestrict qXPcLN7HfLvvFg1KMy6OUiQ8GOTNX9bby27KBNxN0e2N8ye0Dg22Be3WOLoJLJp

