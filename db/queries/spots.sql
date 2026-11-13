-- name: ListSpotsInBBox :many
-- Filters: empty kinds/amenities and NULL min_score = no filter; amenities are AND;
-- min_score excludes spots with no score at ts_hour.
-- include_area adds the park outline simplified to ~5 m (0.00005 deg),
-- coordinates rounded to 6 decimals. NULL for point spots.
SELECT s.id, s.city_pack_id, s.kind, s.name, s.names,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Y(s.geom::geometry)::float8 AS lat,
       s.country, s.city, s.tz, s.last_verified_at,
       (CASE WHEN sqlc.arg(include_area)::bool AND s.area IS NOT NULL
            THEN ST_AsGeoJSON(ST_Multi(ST_SimplifyPreserveTopology(s.area, 0.00005)), 6)::jsonb
       END)::jsonb AS area_geojson,
       sc.score, sc.confidence, sc.cap_reason, sc.reasons, sc.computed_at,
       (SELECT jsonb_agg(jsonb_build_object(
                  'tsHour', to_char(h.ts_hour AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
                  'score', h.score,
                  'capReason', h.cap_reason) ORDER BY h.ts_hour)
          FROM t_spot_scores h
         WHERE h.spot_id = s.id
           AND h.ts_hour >= sqlc.arg(scores_from)::timestamptz
           AND h.ts_hour <  sqlc.arg(scores_to)::timestamptz)::jsonb AS hourly_scores
FROM t_spots s
LEFT JOIN t_spot_scores sc
  ON sc.spot_id = s.id AND sc.ts_hour = sqlc.arg(ts_hour)::timestamptz
WHERE s.status = 'active'
  AND (COALESCE(cardinality(sqlc.arg(kinds)::text[]), 0) = 0 OR s.kind = ANY(sqlc.arg(kinds)::text[]))
  AND (COALESCE(cardinality(sqlc.arg(amenities)::text[]), 0) = 0
       OR (SELECT count(*) FROM t_spot_amenities fa
            WHERE fa.spot_id = s.id AND fa.type = ANY(sqlc.arg(amenities)::text[]))
          = cardinality(sqlc.arg(amenities)::text[]))
  AND (sqlc.narg(min_score)::int IS NULL OR sc.score >= sqlc.narg(min_score)::int)
  AND s.geom::geometry && ST_MakeEnvelope(
      sqlc.arg(west)::float8,
      sqlc.arg(south)::float8,
      sqlc.arg(east)::float8,
      sqlc.arg(north)::float8,
      4326
  );

-- name: ListSpotsByIDs :many
-- Optional filters (kinds / amenities AND / min_score) apply to the resolved target.
-- Same row shape as ListSpotsInBBox. Merged ids resolve one hop to their
-- merge target; unknown / non-active ids are dropped. Duplicates collapse.
WITH wanted AS (
    SELECT DISTINCT COALESCE(m.merged_into, m.id) AS id
    FROM t_spots m
    WHERE m.id = ANY(sqlc.arg(ids)::uuid[])
)
SELECT s.id, s.city_pack_id, s.kind, s.name, s.names,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Y(s.geom::geometry)::float8 AS lat,
       s.country, s.city, s.tz, s.last_verified_at,
       (CASE WHEN sqlc.arg(include_area)::bool AND s.area IS NOT NULL
            THEN ST_AsGeoJSON(ST_Multi(ST_SimplifyPreserveTopology(s.area, 0.00005)), 6)::jsonb
       END)::jsonb AS area_geojson,
       sc.score, sc.confidence, sc.cap_reason, sc.reasons, sc.computed_at,
       (SELECT jsonb_agg(jsonb_build_object(
                  'tsHour', to_char(h.ts_hour AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
                  'score', h.score,
                  'capReason', h.cap_reason) ORDER BY h.ts_hour)
          FROM t_spot_scores h
         WHERE h.spot_id = s.id
           AND h.ts_hour >= sqlc.arg(scores_from)::timestamptz
           AND h.ts_hour <  sqlc.arg(scores_to)::timestamptz)::jsonb AS hourly_scores
FROM t_spots s
JOIN wanted w ON w.id = s.id
LEFT JOIN t_spot_scores sc
  ON sc.spot_id = s.id AND sc.ts_hour = sqlc.arg(ts_hour)::timestamptz
WHERE s.status = 'active'
  AND (COALESCE(cardinality(sqlc.arg(kinds)::text[]), 0) = 0 OR s.kind = ANY(sqlc.arg(kinds)::text[]))
  AND (COALESCE(cardinality(sqlc.arg(amenities)::text[]), 0) = 0
       OR (SELECT count(*) FROM t_spot_amenities fa
            WHERE fa.spot_id = s.id AND fa.type = ANY(sqlc.arg(amenities)::text[]))
          = cardinality(sqlc.arg(amenities)::text[]))
  AND (sqlc.narg(min_score)::int IS NULL OR sc.score >= sqlc.narg(min_score)::int);

-- name: GetSpotByID :one
SELECT s.id, s.city_pack_id, s.kind, s.name, s.names,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Y(s.geom::geometry)::float8 AS lat,
       ST_AsGeoJSON(s.area)::json AS area_geojson,
       s.country, s.city, s.tz, s.cell_id, s.status, s.merged_into,
       s.last_verified_at, s.created_at, s.updated_at
FROM t_spots s
WHERE s.id = $1;

-- name: ListSpotAmenities :many
SELECT type, distance_m, source
FROM t_spot_amenities
WHERE spot_id = $1
ORDER BY type;

-- name: ListSpotSources :many
SELECT source, source_id, version, imported_at
FROM t_spot_sources
WHERE spot_id = $1
ORDER BY source;

-- name: ListCityPacks :many
SELECT id, slug, name, country, tz, status, osm_extract, last_import_at
FROM t_city_packs
WHERE status IN ('beta', 'live')
ORDER BY name;

-- name: GetSpotWeatherProvider :one
-- Provider of the forecast rows scoring reads for this weather cell: the row
-- at the current hour, else the next one, else the latest fetched.
SELECT provider
FROM t_weather_hourly_forecasts
WHERE cell_id = $1
ORDER BY (ts >= date_trunc('hour', now())) DESC,
         CASE WHEN ts >= date_trunc('hour', now()) THEN ts END ASC,
         fetched_at DESC
LIMIT 1;

-- name: ListPackSummaries :many
SELECT p.slug, p.name, p.country, p.tz,
       ST_X(ST_PointOnSurface(p.boundary))::float8 AS center_lon,
       ST_Y(ST_PointOnSurface(p.boundary))::float8 AS center_lat,
       ST_XMin(p.boundary)::float8 AS west,
       ST_YMin(p.boundary)::float8 AS south,
       ST_XMax(p.boundary)::float8 AS east,
       ST_YMax(p.boundary)::float8 AS north,
       (SELECT count(*) FROM t_spots s
         WHERE s.city_pack_id = p.id AND s.status = 'active')::int AS active_spots
FROM t_city_packs p
WHERE p.status IN ('beta', 'live')
ORDER BY p.country, p.name;
