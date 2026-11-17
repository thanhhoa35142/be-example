-- Read-only role for the MCP `db_query` tool.
--
-- Creates role `mcp_ro`: SELECT on an explicit allowlist of operational
-- tables only, default_transaction_read_only=on. Tables holding secrets or
-- user data (t_api_keys, t_devices, t_subscriptions, t_admin_*,
-- t_apple_notifications, t_alert_*, t_weekly_digests) are intentionally
-- NOT granted — new tables are invisible until added here and re-run.
--
-- Run once per database (needs a role that can CREATE ROLE + GRANT, e.g. the
-- migration user):
--
--   MCP_RO_PASSWORD='...' psql "$DATABASE_URL" -v ro_password="$MCP_RO_PASSWORD" -f db/mcp-ro.sql
--
-- Then point the MCP server at it:
--   MCP_RO_DATABASE_URL=postgres://mcp_ro:<pw>@host:port/db?sslmode=disable

\set ON_ERROR_STOP on

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'mcp_ro') THEN
        CREATE ROLE mcp_ro LOGIN;
    END IF;
END $$;

ALTER ROLE mcp_ro WITH LOGIN PASSWORD :'ro_password';
ALTER ROLE mcp_ro SET default_transaction_read_only = on;
ALTER ROLE mcp_ro SET statement_timeout = '30s';

DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO mcp_ro', current_database());
END $$;

GRANT USAGE ON SCHEMA public TO mcp_ro;

GRANT SELECT ON
    t_city_packs,
    t_spots,
    t_spot_amenities,
    t_spot_sources,
    t_spot_scores,
    t_scoring_configs,
    t_weather_hourly_forecasts,
    t_crowd_curves,
    t_crowd_estimates,
    t_crowd_reports,
    t_spot_reports,
    t_score_feedbacks,
    t_events,
    t_job_runs,
    t_worker_job_schedules,
    t_provider_quotas,
    t_plan_limits,
    t_holidays,
    t_ingest_features,
    t_state_transitions
TO mcp_ro;
