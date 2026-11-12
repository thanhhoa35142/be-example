-- name: DistinctSpotCells :many
SELECT DISTINCT cell_id
FROM t_spots
WHERE status = 'active';

-- name: UpsertWeatherHourly :copyfrom
INSERT INTO t_weather_hourly_forecasts (
    cell_id, ts, temp, feels_like, precip_prob, wind, uv, cloud,
    provider, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: DeleteWeatherBefore :exec
DELETE FROM t_weather_hourly_forecasts
WHERE ts < $1;

-- name: DeleteWeatherCellRange :exec
DELETE FROM t_weather_hourly_forecasts
WHERE cell_id = $1 AND ts >= $2 AND ts <= $3;

-- name: ReserveProviderQuota :one
-- Atomically adds `weight` to the provider's UTC-day budget, but only while
-- the result stays within `limit`. The INSERT branch is guarded by
-- `SELECT ... WHERE weight <= limit` so a first call of the day cannot exceed
-- the cap either. Returns the new `used` value; no row is returned
-- (sqlc.ErrNoRows) when the budget would be exceeded.
INSERT INTO t_provider_quotas (provider, day, used)
SELECT sqlc.arg(provider), (now() AT TIME ZONE 'UTC')::date, sqlc.arg(weight)
WHERE sqlc.arg(weight) <= sqlc.arg(quota_limit)::integer
ON CONFLICT (provider, day) DO UPDATE
SET used = t_provider_quotas.used + EXCLUDED.used,
    updated_at = now()
WHERE t_provider_quotas.used + EXCLUDED.used <= sqlc.arg(quota_limit)::integer
RETURNING used;
