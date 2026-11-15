-- name: GetActiveScoringConfig :one
SELECT version, weights, thresholds
FROM t_scoring_configs
WHERE active_from <= now()
ORDER BY active_from DESC, version DESC
LIMIT 1;

-- name: ListActiveSpotsForScoring :many
SELECT id, kind, cell_id, tz, last_verified_at,
       ST_Y(geom::geometry)::float8 AS lat, ST_X(geom::geometry)::float8 AS lon
FROM t_spots
WHERE status = 'active';

-- name: ListAmenitiesForSpots :many
SELECT spot_id, type, distance_m
FROM t_spot_amenities
WHERE spot_id = ANY($1::uuid[]);

-- name: ListWeatherForCells :many
SELECT cell_id, ts, temp, feels_like, precip_prob, wind, uv, cloud
FROM t_weather_hourly_forecasts
WHERE cell_id = ANY($1::bigint[])
  AND ts >= $2
  AND ts < $3;

-- name: ListCrowdEstimatesForSpots :many
SELECT spot_id, dow, hour, level, source
FROM t_crowd_estimates
WHERE spot_id = ANY($1::uuid[]);

-- name: UpsertSpotScoresBatch :execrows
-- Batched upsert (one statement per ~10k rows). Nullable columns arrive as
-- sentinels because sqlc array params cannot carry NULL: -1 for the smallint
-- components, '' for cap_reason. reasons arrive as JSON text.
-- Unchanged rows are NOT rewritten (no new tuple version, no WAL, no index
-- churn): computed_at therefore means "last time the score value changed",
-- not "last time the job looked at it". The returned count is rows
-- inserted or actually changed.
INSERT INTO t_spot_scores (
    spot_id, ts_hour, config_version, score,
    s_weather, s_crowd, s_amenity, s_shade, s_fresh,
    cap_reason, reasons, confidence, computed_at
)
SELECT (sqlc.arg(spot_ids)::uuid[])[i], (sqlc.arg(ts_hours)::timestamptz[])[i],
       sqlc.arg(config_version)::smallint, (sqlc.arg(scores)::smallint[])[i],
       NULLIF((sqlc.arg(s_weather)::smallint[])[i], -1),
       NULLIF((sqlc.arg(s_crowd)::smallint[])[i], -1),
       NULLIF((sqlc.arg(s_amenity)::smallint[])[i], -1),
       NULLIF((sqlc.arg(s_shade)::smallint[])[i], -1),
       NULLIF((sqlc.arg(s_fresh)::smallint[])[i], -1),
       NULLIF(cr.v, ''),
       rs.v::jsonb,
       (sqlc.arg(confidences)::smallint[])[i],
       sqlc.arg(computed_at)::timestamptz
FROM generate_series(1, cardinality(sqlc.arg(spot_ids)::uuid[])) AS i
-- Variable-length arrays (text) are joined via WITH ORDINALITY: arr[i] on them
-- is O(i) per access, i.e. quadratic per batch. Fixed-width arrays subscript in O(1).
JOIN unnest(sqlc.arg(cap_reasons)::text[]) WITH ORDINALITY AS cr(v, n) ON cr.n = i
JOIN unnest(sqlc.arg(reasons)::text[]) WITH ORDINALITY AS rs(v, n) ON rs.n = i
ON CONFLICT (spot_id, ts_hour) DO UPDATE SET
    config_version = EXCLUDED.config_version,
    score          = EXCLUDED.score,
    s_weather      = EXCLUDED.s_weather,
    s_crowd        = EXCLUDED.s_crowd,
    s_amenity      = EXCLUDED.s_amenity,
    s_shade        = EXCLUDED.s_shade,
    s_fresh        = EXCLUDED.s_fresh,
    cap_reason     = EXCLUDED.cap_reason,
    reasons        = EXCLUDED.reasons,
    confidence     = EXCLUDED.confidence,
    computed_at    = EXCLUDED.computed_at
WHERE (t_spot_scores.config_version, t_spot_scores.score,
       t_spot_scores.s_weather, t_spot_scores.s_crowd, t_spot_scores.s_amenity,
       t_spot_scores.s_shade, t_spot_scores.s_fresh,
       t_spot_scores.cap_reason, t_spot_scores.reasons, t_spot_scores.confidence)
      IS DISTINCT FROM
      (EXCLUDED.config_version, EXCLUDED.score,
       EXCLUDED.s_weather, EXCLUDED.s_crowd, EXCLUDED.s_amenity,
       EXCLUDED.s_shade, EXCLUDED.s_fresh,
       EXCLUDED.cap_reason, EXCLUDED.reasons, EXCLUDED.confidence);
