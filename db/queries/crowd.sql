-- name: ListCrowdSpots :many
SELECT s.id, s.kind, s.city_pack_id, s.country, s.cell_id, s.tz,
       COALESCE(ST_Area(s.area::geography), 0)::float8 AS area_m2
FROM spot s
WHERE s.status = 'active';

-- name: ListCrowdCurves :many
SELECT kind, dow, hour, factor
FROM crowd_curve
WHERE config_version = (SELECT max(config_version) FROM crowd_curve);

-- name: ListCrowdWeather :many
SELECT cell_id, ts, temp, feels_like, precip_prob
FROM weather_hourly
WHERE cell_id = ANY($1::bigint[])
  AND ts >= $2
  AND ts < $3;

-- name: ListCrowdHolidays :many
SELECT country, date, kind
FROM holiday
WHERE country::text = ANY($1::text[])
  AND date >= $2
  AND date <= $3;

-- name: UpsertCrowdEstimate :exec
INSERT INTO crowd_estimate (spot_id, dow, hour, level, source, n_reports, updated_at)
VALUES ($1, $2, $3, $4, 'model', 0, now())
ON CONFLICT (spot_id, dow, hour) DO UPDATE SET
    level      = EXCLUDED.level,
    source     = EXCLUDED.source,
    n_reports  = 0,
    updated_at = EXCLUDED.updated_at
WHERE crowd_estimate.source = 'model';
