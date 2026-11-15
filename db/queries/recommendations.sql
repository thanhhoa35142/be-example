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
  AND (COALESCE(cardinality(sqlc.arg(kinds)::text[]), 0) = 0 OR s.kind = ANY(sqlc.arg(kinds)::text[]))
  AND (COALESCE(cardinality(sqlc.arg(amenities)::text[]), 0) = 0
       OR (SELECT count(*) FROM t_spot_amenities fa
            WHERE fa.spot_id = s.id AND fa.type = ANY(sqlc.arg(amenities)::text[]))
          = cardinality(sqlc.arg(amenities)::text[]))
  AND (sqlc.narg(min_score)::int IS NULL OR sc.score >= sqlc.narg(min_score)::int)
ORDER BY sc.score DESC NULLS LAST, distance_m ASC
LIMIT $5;
