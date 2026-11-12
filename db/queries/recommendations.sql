-- name: ListRecommendedSpots :many
SELECT s.id, s.kind, s.name, s.names, s.country, s.city, s.tz, s.last_verified_at,
       ST_Y(s.geom::geometry)::float8 AS lat,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Distance(
           s.geom,
           ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography
       )::float8 AS distance_m,
       sc.score, sc.confidence, sc.cap_reason, sc.reasons, sc.computed_at
FROM t_spots s
LEFT JOIN t_spot_scores sc
       ON sc.spot_id = s.id AND sc.ts_hour = $3
WHERE s.status = 'active'
  AND ST_DWithin(
      s.geom,
      ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
      $4
  )
ORDER BY sc.score DESC NULLS LAST, distance_m ASC
LIMIT $5;
