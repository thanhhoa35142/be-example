-- Getaways (docs/GETAWAYS_SPEC.md). t_city_hour_outlook is the hourly city
-- weather series; windows are computed at read time.

-- name: ListGetawayPacks :many
-- Beta/live packs with their active spot count (eligibility needs >= 30).
SELECT p.id, p.slug, p.name, p.country, p.tz,
       (SELECT count(*) FROM t_spots s
         WHERE s.city_pack_id = p.id AND s.status = 'active')::int AS active_spots
FROM t_city_packs p
WHERE p.status IN ('beta', 'live')
ORDER BY p.slug;

-- name: ComputeCityHours :many
-- City-hour weather score for one pack (spec §3.1): median over active spots
-- of LEAST(s_weather, cap value of cap_reason); 100 when uncapped. Start
-- hours 08:00-19:00 local. The total score only picks the top spot: among
-- named spots only (same filter as the nudge query; an unnamed spot is no
-- place to send someone), ties by name then id. An hour with no named spot
-- keeps its row with a NULL top spot.
WITH spot_hours AS (
    SELECT ss.ts_hour, ss.spot_id, ss.score, s.name,
           COALESCE(ss.cap_reason = 'rain', false) AS rainy,
           (ss.ts_hour AT TIME ZONE p.tz)::date AS local_date,
           LEAST(ss.s_weather::int, CASE ss.cap_reason
               WHEN 'rain'     THEN @rain_cap::int
               WHEN 'heat'     THEN @heat_cap::int
               WHEN 'cold'     THEN @cold_cap::int
               WHEN 'dark'     THEN @dark_cap::int
               WHEN 'twilight' THEN @twilight_cap::int
               ELSE 100 END) AS w
    FROM t_spot_scores ss
    JOIN t_spots s ON s.id = ss.spot_id AND s.status = 'active'
    JOIN t_city_packs p ON p.id = s.city_pack_id
    WHERE p.id = @pack_id
      AND ss.s_weather IS NOT NULL
      AND ss.ts_hour >= @from_ts::timestamptz
      AND ss.ts_hour < @to_ts::timestamptz
      AND EXTRACT(HOUR FROM ss.ts_hour AT TIME ZONE p.tz) BETWEEN 8 AND 19
), agg AS (
    SELECT ts_hour, local_date,
           round(percentile_cont(0.5) WITHIN GROUP (ORDER BY w))::smallint AS weather_score,
           (avg(rainy::int))::real AS rain_share,
           count(*)::int AS spots_n
    FROM spot_hours
    GROUP BY ts_hour, local_date
), top AS (
    SELECT DISTINCT ON (ts_hour) ts_hour, spot_id, score
    FROM spot_hours
    WHERE name IS NOT NULL AND btrim(name) <> ''
    ORDER BY ts_hour, score DESC, name ASC, spot_id
)
SELECT a.ts_hour, a.local_date, a.weather_score, a.rain_share, a.spots_n,
       t.spot_id AS top_spot_id, t.score AS top_spot_score
FROM agg a
LEFT JOIN top t ON t.ts_hour = a.ts_hour
ORDER BY a.ts_hour;

-- name: DeleteOutlookFrom :exec
DELETE FROM t_city_hour_outlook WHERE pack_id = @pack_id AND ts_hour >= @from_ts::timestamptz;

-- name: InsertOutlookHours :exec
INSERT INTO t_city_hour_outlook
    (pack_id, ts_hour, local_date, weather_score, rain_share, spots_n, top_spot_id, top_spot_score, config_version, computed_at)
-- The nil uuid stands for 'no named spot this hour' and is stored as NULL.
SELECT @pack_id::uuid, u.ts_hour, u.local_date, u.weather_score, u.rain_share, u.spots_n,
       NULLIF(u.top_spot_id, '00000000-0000-0000-0000-000000000000'::uuid),
       CASE WHEN u.top_spot_id = '00000000-0000-0000-0000-000000000000'::uuid THEN NULL ELSE u.top_spot_score END,
       @config_version::smallint, @computed_at::timestamptz
FROM (SELECT unnest(@ts_hours::timestamptz[])       AS ts_hour,
             unnest(@local_dates::date[])           AS local_date,
             unnest(@weather_scores::smallint[])    AS weather_score,
             unnest(@rain_shares::real[])           AS rain_share,
             unnest(@spots_ns::int[])               AS spots_n,
             unnest(@top_spot_ids::uuid[])          AS top_spot_id,
             unnest(@top_spot_scores::smallint[])   AS top_spot_score) u
ON CONFLICT (pack_id, ts_hour) DO UPDATE SET
    local_date = EXCLUDED.local_date,
    weather_score = EXCLUDED.weather_score,
    rain_share = EXCLUDED.rain_share,
    spots_n = EXCLUDED.spots_n,
    top_spot_id = EXCLUDED.top_spot_id,
    top_spot_score = EXCLUDED.top_spot_score,
    config_version = EXCLUDED.config_version,
    computed_at = EXCLUDED.computed_at;

-- name: DeleteOutlookExceptPacks :execrows
-- Rows of packs that are no longer eligible (retired, or under the spot minimum).
DELETE FROM t_city_hour_outlook WHERE pack_id <> ALL(@keep::uuid[]);

-- name: DeleteOutlookBefore :execrows
DELETE FROM t_city_hour_outlook WHERE ts_hour < @before::timestamptz;

-- name: ListOutlookHours :many
-- Hours of beta/live packs whose local date is in [from_date, to_date] and
-- that start at or after not_before.
SELECT o.pack_id, p.slug, p.name, p.country, p.tz,
       o.ts_hour, o.local_date, o.weather_score, o.rain_share,
       o.top_spot_id, o.top_spot_score, o.computed_at
FROM t_city_hour_outlook o
JOIN t_city_packs p ON p.id = o.pack_id AND p.status IN ('beta', 'live')
WHERE o.local_date BETWEEN @from_date::date AND @to_date::date
  AND o.ts_hour >= @not_before::timestamptz
ORDER BY p.slug, o.ts_hour;

-- name: ListPackCenters :many
SELECT p.id,
       ST_X(ST_PointOnSurface(p.boundary))::float8 AS center_lon,
       ST_Y(ST_PointOnSurface(p.boundary))::float8 AS center_lat
FROM t_city_packs p
WHERE p.id = ANY(@ids::uuid[]);

-- name: ListGetawaySpots :many
SELECT id, name FROM t_spots WHERE id = ANY(@ids::uuid[]);

-- name: GetSpotScoreReasons :one
SELECT reasons FROM t_spot_scores WHERE spot_id = @spot_id AND ts_hour = @ts_hour::timestamptz;
