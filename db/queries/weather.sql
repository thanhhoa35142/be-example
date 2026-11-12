-- name: DistinctSpotCells :many
SELECT DISTINCT cell_id
FROM spot
WHERE status = 'active';

-- name: UpsertWeatherHourly :copyfrom
INSERT INTO weather_hourly (
    cell_id, ts, temp, feels_like, precip_prob, wind, uv, cloud,
    provider, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: DeleteWeatherBefore :exec
DELETE FROM weather_hourly
WHERE ts < $1;

-- name: DeleteWeatherCellRange :exec
DELETE FROM weather_hourly
WHERE cell_id = $1 AND ts >= $2 AND ts <= $3;

-- name: ConsumeProviderQuota :one
-- Atomically adds `weight` to today's usage for `provider`, but only while
-- the result stays within `limit`. Returns the new `used` value; no row is
-- returned (sqlc.ErrNoRows) when the budget would be exceeded.
INSERT INTO provider_quota (provider, day, used)
VALUES (sqlc.arg(provider), CURRENT_DATE, sqlc.arg(weight))
ON CONFLICT (provider, day) DO UPDATE
SET used = provider_quota.used + EXCLUDED.used,
    updated_at = now()
WHERE provider_quota.used + EXCLUDED.used <= sqlc.arg(quota_limit)
RETURNING used;
