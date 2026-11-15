-- name: ListSpotForecast :many
-- Hourly scores plus the raw weather of the spot's weather cell for the same
-- hour (NULL columns when that hour has no weather row).
SELECT ss.ts_hour, ss.score, ss.s_weather, ss.s_crowd, ss.s_amenity, ss.s_shade, ss.s_fresh,
       ss.cap_reason, ss.reasons, ss.confidence, ss.computed_at,
       w.temp, w.feels_like, w.precip_prob, w.wind, w.uv, w.cloud
FROM t_spot_scores ss
LEFT JOIN t_weather_hourly_forecasts w
       ON w.cell_id = sqlc.arg(weather_cell_id)::bigint AND w.ts = ss.ts_hour
WHERE ss.spot_id = sqlc.arg(spot_id)
  AND ss.ts_hour >= sqlc.arg(from_ts)
  AND ss.ts_hour < sqlc.arg(to_ts)
ORDER BY ss.ts_hour;
