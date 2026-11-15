-- Google traffic sync: Search Console + GA4 data pulled daily by the
-- google.sync worker job using the linked admin's OAuth grant.
--
-- t_admin_google_tokens gains the per-link sync config (which GSC site and
-- GA4 property to pull); empty string means "not configured yet".
ALTER TABLE t_admin_google_tokens
    ADD COLUMN gsc_site_url    text NOT NULL DEFAULT '',
    ADD COLUMN ga4_property_id text NOT NULL DEFAULT '';

-- Daily GSC totals per site (all queries+pages aggregated by Google).
CREATE TABLE t_traffic_gsc_daily (
    date         date             NOT NULL,
    site_url     text             NOT NULL,
    clicks       bigint           NOT NULL,
    impressions  bigint           NOT NULL,
    ctr          double precision NOT NULL,
    position     double precision NOT NULL,
    synced_at    timestamptz      NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_traffic_gsc_daily PRIMARY KEY (date, site_url)
);

-- Snapshot of top queries/pages over the synced window. Rewritten on every
-- sync, so it always reflects the latest pull rather than accumulating
-- history per key.
CREATE TABLE t_traffic_gsc_dims (
    site_url      text             NOT NULL,
    dimension     text             NOT NULL,
    key           text             NOT NULL,
    clicks        bigint           NOT NULL,
    impressions   bigint           NOT NULL,
    ctr           double precision NOT NULL,
    position      double precision NOT NULL,
    period_start  date             NOT NULL,
    period_end    date             NOT NULL,
    synced_at     timestamptz      NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_traffic_gsc_dims PRIMARY KEY (site_url, dimension, key)
);

-- Daily GA4 totals per property.
CREATE TABLE t_traffic_ga4_daily (
    date                      date             NOT NULL,
    property_id               text             NOT NULL,
    sessions                  bigint           NOT NULL,
    total_users               bigint           NOT NULL,
    new_users                 bigint           NOT NULL,
    page_views                bigint           NOT NULL,
    engagement_rate           double precision,
    avg_session_duration_sec  double precision,
    synced_at                 timestamptz      NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_traffic_ga4_daily PRIMARY KEY (date, property_id)
);

CREATE INDEX idx_t_traffic_gsc_dims_clicks
    ON t_traffic_gsc_dims (dimension, clicks DESC);
CREATE INDEX idx_t_traffic_ga4_daily_date
    ON t_traffic_ga4_daily (date DESC);

INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('google.sync', 86400, true, 420)
ON CONFLICT (job) DO NOTHING;
