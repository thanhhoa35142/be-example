-- name: ListSpotForecast :many
SELECT ts_hour, score, s_weather, s_crowd, s_amenity, s_shade, s_fresh,
       cap_reason, reasons, confidence, computed_at
FROM t_spot_scores
WHERE spot_id = $1
  AND ts_hour >= $2
  AND ts_hour < $3
ORDER BY ts_hour;
