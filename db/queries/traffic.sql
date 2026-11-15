-- name: UpsertAdminGoogleTokenConfig :exec
UPDATE t_admin_google_tokens
SET gsc_site_url = $2, ga4_property_id = $3, updated_at = now()
WHERE admin_user_id = $1;

-- name: ListAdminGoogleTokens :many
SELECT admin_user_id, google_sub, google_email, refresh_token, scopes,
       gsc_site_url, ga4_property_id, created_at, updated_at
FROM t_admin_google_tokens;

-- name: UpsertTrafficGscDaily :exec
INSERT INTO t_traffic_gsc_daily (date, site_url, clicks, impressions, ctr, position)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (date, site_url) DO UPDATE
SET clicks = EXCLUDED.clicks,
    impressions = EXCLUDED.impressions,
    ctr = EXCLUDED.ctr,
    position = EXCLUDED.position,
    synced_at = now();

-- name: DeleteTrafficGscDims :exec
DELETE FROM t_traffic_gsc_dims WHERE site_url = $1 AND dimension = $2;

-- name: UpsertTrafficGscDim :exec
INSERT INTO t_traffic_gsc_dims
    (site_url, dimension, key, clicks, impressions, ctr, position, period_start, period_end)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (site_url, dimension, key) DO UPDATE
SET clicks = EXCLUDED.clicks,
    impressions = EXCLUDED.impressions,
    ctr = EXCLUDED.ctr,
    position = EXCLUDED.position,
    period_start = EXCLUDED.period_start,
    period_end = EXCLUDED.period_end,
    synced_at = now();

-- name: UpsertTrafficGa4Daily :exec
INSERT INTO t_traffic_ga4_daily
    (date, property_id, sessions, total_users, new_users, page_views,
     engagement_rate, avg_session_duration_sec)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (date, property_id) DO UPDATE
SET sessions = EXCLUDED.sessions,
    total_users = EXCLUDED.total_users,
    new_users = EXCLUDED.new_users,
    page_views = EXCLUDED.page_views,
    engagement_rate = EXCLUDED.engagement_rate,
    avg_session_duration_sec = EXCLUDED.avg_session_duration_sec,
    synced_at = now();

-- Admin reads: aggregated across all configured sites/properties so the
-- console shows one traffic view regardless of how many links exist.

-- name: ListTrafficGscDaily :many
SELECT date,
       sum(clicks)::bigint       AS clicks,
       sum(impressions)::bigint  AS impressions,
       avg(ctr)                  AS ctr,
       avg(position)             AS position
FROM t_traffic_gsc_daily
WHERE date >= $1
GROUP BY date
ORDER BY date;

-- name: ListTrafficGscDims :many
SELECT dimension, key,
       sum(clicks)::bigint      AS clicks,
       sum(impressions)::bigint AS impressions,
       avg(ctr)                 AS ctr,
       avg(position)            AS position
FROM t_traffic_gsc_dims
WHERE dimension = $1
GROUP BY dimension, key
ORDER BY clicks DESC
LIMIT $2;

-- name: ListTrafficGscDimWindow :one
SELECT period_start, period_end
FROM t_traffic_gsc_dims
WHERE dimension = $1
ORDER BY synced_at DESC
LIMIT 1;

-- name: ListTrafficGa4Daily :many
SELECT date,
       sum(sessions)::bigint     AS sessions,
       sum(total_users)::bigint  AS total_users,
       sum(new_users)::bigint    AS new_users,
       sum(page_views)::bigint   AS page_views,
       COALESCE(avg(engagement_rate), 0)::float8          AS engagement_rate,
       COALESCE(avg(avg_session_duration_sec), 0)::float8 AS avg_session_duration_sec
FROM t_traffic_ga4_daily
WHERE date >= $1
GROUP BY date
ORDER BY date;

-- name: TrafficGa4Totals :one
SELECT COALESCE(sum(sessions), 0)::bigint    AS sessions,
       COALESCE(sum(total_users), 0)::bigint AS total_users,
       COALESCE(sum(new_users), 0)::bigint   AS new_users,
       COALESCE(sum(page_views), 0)::bigint  AS page_views
FROM t_traffic_ga4_daily
WHERE date >= $1;
