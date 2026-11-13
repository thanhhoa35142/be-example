-- name: ListCrowdSpots :many
SELECT s.id, s.kind, s.city_pack_id, s.country, s.cell_id, s.tz,
       COALESCE(ST_Area(s.area::geography), 0)::float8 AS area_m2
FROM t_spots s
WHERE s.status = 'active';

-- name: ListCrowdCurves :many
SELECT kind, dow, hour, factor
FROM t_crowd_curves
WHERE config_version = (SELECT max(config_version) FROM t_crowd_curves);

-- name: ListCrowdWeather :many
SELECT cell_id, ts, temp, feels_like, precip_prob
FROM t_weather_hourly_forecasts
WHERE cell_id = ANY($1::bigint[])
  AND ts >= $2
  AND ts < $3;

-- name: ListCrowdHolidays :many
SELECT country, date, kind
FROM t_holidays
WHERE country::text = ANY($1::text[])
  AND date >= $2
  AND date <= $3;

-- name: UpsertCrowdEstimatesBatch :execrows
-- Batched model upsert (~10k rows per statement). Never overwrites community
-- or provider rows, and skips model rows whose level is unchanged, so
-- updated_at means "last time the model level changed" (the freshness
-- endpoint also considers the last successful crowd.refresh run).
INSERT INTO t_crowd_estimates (spot_id, dow, hour, level, source, n_reports, updated_at)
SELECT (sqlc.arg(spot_ids)::uuid[])[i], (sqlc.arg(dows)::smallint[])[i],
       (sqlc.arg(hours)::smallint[])[i], (sqlc.arg(levels)::smallint[])[i],
       'model', 0, now()
FROM generate_series(1, cardinality(sqlc.arg(spot_ids)::uuid[])) AS i
ON CONFLICT (spot_id, dow, hour) DO UPDATE SET
    level      = EXCLUDED.level,
    source     = EXCLUDED.source,
    n_reports  = 0,
    updated_at = EXCLUDED.updated_at
WHERE t_crowd_estimates.source = 'model'
  AND t_crowd_estimates.level IS DISTINCT FROM EXCLUDED.level;

-- name: ListHolidayCountries :many
SELECT DISTINCT country::text AS country
FROM t_city_packs
WHERE status <> 'retired'
ORDER BY 1;

-- name: UpsertHolidays :execrows
-- rows: JSON array of {country, date (YYYY-MM-DD), name}; national public
-- holidays only (region '').
INSERT INTO t_holidays (country, region, date, kind, name)
SELECT e->>'country', '', (e->>'date')::date, 'public', e->>'name'
FROM jsonb_array_elements(sqlc.arg(rows)::jsonb) AS e
ON CONFLICT (country, region, date, kind) DO UPDATE SET name = EXCLUDED.name;
