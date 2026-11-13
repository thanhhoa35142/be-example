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

-- name: UpsertSpotScore :execrows
INSERT INTO t_spot_scores (
    spot_id, ts_hour, config_version, score,
    s_weather, s_crowd, s_amenity, s_shade, s_fresh,
    cap_reason, reasons, confidence, computed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
WHERE t_spot_scores.score          IS DISTINCT FROM EXCLUDED.score
   OR t_spot_scores.reasons        IS DISTINCT FROM EXCLUDED.reasons
   OR t_spot_scores.confidence     IS DISTINCT FROM EXCLUDED.confidence
   OR t_spot_scores.computed_at    IS DISTINCT FROM EXCLUDED.computed_at;
