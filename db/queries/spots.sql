-- name: ListSpotsInBBox :many
SELECT s.id, s.city_pack_id, s.kind, s.name, s.names,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Y(s.geom::geometry)::float8 AS lat,
       s.country, s.city, s.tz, s.last_verified_at,
       sc.score, sc.confidence, sc.cap_reason, sc.reasons, sc.computed_at
FROM t_spots s
LEFT JOIN t_spot_scores sc
  ON sc.spot_id = s.id AND sc.ts_hour = sqlc.arg(ts_hour)::timestamptz
WHERE s.status = 'active'
  AND s.geom::geometry && ST_MakeEnvelope(
      sqlc.arg(west)::float8,
      sqlc.arg(south)::float8,
      sqlc.arg(east)::float8,
      sqlc.arg(north)::float8,
      4326
  );

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
