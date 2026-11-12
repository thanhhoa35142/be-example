-- name: ListIngestPacks :many
SELECT id, slug, name, country, tz, osm_extract, boundary
FROM t_city_packs
WHERE status IN ('beta','live')
ORDER BY slug;

-- name: PackBBox :one
SELECT ST_XMin(boundary), ST_YMin(boundary), ST_XMax(boundary), ST_YMax(boundary)
FROM t_city_packs WHERE id = $1;

-- name: ClearIngestFeatures :exec
DELETE FROM t_ingest_features WHERE pack_id = $1;

-- name: InsertIngestFeature :exec
INSERT INTO t_ingest_features
    (pack_id, source, source_id, role, kind, amenity, quality,
     exclude_reason, version, tags, geom, content_hash, run_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
        $10::jsonb, ST_GeomFromGeoJSON($11), $12, $13);

-- name: ListCandidateFeatures :many
SELECT source, source_id, kind, quality, version, tags, content_hash,
       ST_AsGeoJSON(geom) AS geojson,
       ST_X(ST_Centroid(geom)) AS lon,
       ST_Y(ST_Centroid(geom)) AS lat,
       CASE WHEN ST_Dimension(geom) >= 2 THEN ST_AsGeoJSON(geom) END AS polygon_geojson
FROM t_ingest_features
WHERE pack_id = $1 AND role = 'candidate'
ORDER BY source_id;

-- name: ListAmenityFeatures :many
SELECT amenity, source, source_id,
       ST_X(ST_Centroid(geom)) AS lon,
       ST_Y(ST_Centroid(geom)) AS lat
FROM t_ingest_features
WHERE pack_id = $1 AND role = 'amenity';

-- name: FindSpotBySource :one
SELECT spot_id, version FROM t_spot_sources
WHERE source = $1 AND source_id = $2;

-- name: ListAbsorbedCandidates :many
-- Child candidates (site/table) whose centroid falls inside a park/beach
-- candidate polygon — they become evidence/sources of the parent, not spots.
SELECT c.source, c.source_id,
       p.source AS parent_source, p.source_id AS parent_source_id
FROM t_ingest_features c
JOIN t_ingest_features p
  ON p.pack_id = c.pack_id AND p.role = 'candidate'
 AND p.kind IN ('park','beach')
 AND ST_GeometryType(p.geom) IN ('ST_Polygon','ST_MultiPolygon')
 AND ST_Contains(p.geom, ST_Centroid(c.geom))
WHERE c.pack_id = $1 AND c.role = 'candidate'
  AND c.kind IN ('site','table')
  AND NOT (c.source = p.source AND c.source_id = p.source_id);

-- name: ClusterOrphanTables :many
-- Orphan table candidates (not absorbed into a park/beach polygon) clustered
-- via ST_ClusterDBSCAN in Web Mercator. $2 is eps already adjusted to
-- mercator meters by the caller (real meters / cos(latitude)); $3 is
-- minpoints. cluster_id < 0 noise rows are dropped.
WITH orphan AS (
    SELECT f.source, f.source_id, f.version,
           ST_X(ST_Centroid(f.geom)) AS lon,
           ST_Y(ST_Centroid(f.geom)) AS lat,
           ST_Centroid(f.geom) AS geom
    FROM t_ingest_features f
    WHERE f.pack_id = $1 AND f.role = 'candidate' AND f.kind = 'table'
      AND NOT EXISTS (
          SELECT 1 FROM t_ingest_features p
          WHERE p.pack_id = f.pack_id AND p.role = 'candidate'
            AND p.kind IN ('park','beach')
            AND ST_GeometryType(p.geom) IN ('ST_Polygon','ST_MultiPolygon')
            AND ST_Contains(p.geom, ST_Centroid(f.geom))
            AND NOT (p.source = f.source AND p.source_id = f.source_id))
),
clustered AS (
    SELECT o.*,
           ST_ClusterDBSCAN(ST_Transform(o.geom, 3857), $2, $3) OVER () AS cluster_id
    FROM orphan o
)
SELECT source, source_id, version, lon, lat, cluster_id
FROM clustered
WHERE cluster_id >= 0
ORDER BY cluster_id, source_id;

-- name: ListPackSpots :many
SELECT id, status, quality, curated, merged_into, kind, name, names,
       attrs, country, tz, cell_id, status_reason,
       ST_X(geom::geometry) AS lon, ST_Y(geom::geometry) AS lat,
       CASE WHEN area IS NOT NULL THEN ST_AsGeoJSON(area) END AS area_geojson
FROM t_spots
WHERE city_pack_id = $1;

-- name: InsertIngestSpot :one
INSERT INTO t_spots (city_pack_id, kind, name, names, geom, area, country,
                     city, tz, cell_id, status, quality, attrs,
                     status_reason, status_changed_at)
VALUES ($1, $2, $3, $4::jsonb,
        ST_SetSRID(ST_MakePoint($5, $6), 4326)::geography,
        CASE WHEN sqlc.narg('area')::text IS NOT NULL
             THEN ST_Multi(ST_GeomFromGeoJSON(sqlc.narg('area'))) END,
        $7, NULL, $8, $9, $10, $11, $12::jsonb, $13, now())
RETURNING id;

-- name: UpdateIngestSpot :exec
UPDATE t_spots SET
    name = $2, names = $3::jsonb, attrs = $4::jsonb,
    quality = $5, geom = ST_SetSRID(ST_MakePoint($6, $7), 4326)::geography,
    area = CASE WHEN sqlc.narg('area')::text IS NOT NULL
                THEN ST_Multi(ST_GeomFromGeoJSON(sqlc.narg('area'))) ELSE area END,
    updated_at = now()
WHERE id = $1;

-- name: SetSpotStatus :exec
UPDATE t_spots SET status = $2, status_reason = $3, updated_at = now()
WHERE id = $1;

-- name: UpsertSpotSource :exec
INSERT INTO t_spot_sources (spot_id, source, source_id, version,
                            imported_at, last_seen_at, missing_runs)
VALUES ($1, $2, $3, $4, now(), now(), 0)
ON CONFLICT (source, source_id) DO UPDATE SET
    spot_id = EXCLUDED.spot_id,
    version = EXCLUDED.version,
    last_seen_at = now(),
    missing_runs = 0;

-- name: ResetSourceSightings :exec
-- Sources present in this run's raw layer get missing_runs reset.
UPDATE t_spot_sources ss SET missing_runs = 0, last_seen_at = now()
FROM t_spots sp
JOIN t_ingest_features f
  ON f.pack_id = sp.city_pack_id
 AND f.role IN ('candidate','amenity')
WHERE sp.id = ss.spot_id
  AND sp.city_pack_id = $1
  AND f.source = ss.source AND f.source_id = ss.source_id;

-- name: BumpMissingSources :many
-- Sources of this pack's spots absent from the raw layer this run.
UPDATE t_spot_sources ss SET missing_runs = ss.missing_runs + 1
FROM t_spots sp
WHERE sp.id = ss.spot_id AND sp.city_pack_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM t_ingest_features f
      WHERE f.pack_id = $1 AND f.source = ss.source AND f.source_id = ss.source_id
        AND f.role IN ('candidate','amenity'))
RETURNING ss.spot_id, ss.missing_runs;

-- name: CountRecentSpotConfirms :one
SELECT count(*) FROM t_spot_reports
WHERE spot_id = $1 AND status = 'accepted'
  AND created_at > now() - ($2 || ' days')::interval;

-- name: NearestSpotForPoint :one
SELECT s.id, ST_Distance(s.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) AS dist_m
FROM t_spots s
WHERE s.city_pack_id = $3 AND s.status = 'active'
  AND s.area IS NOT NULL
  AND ST_DWithin(s.area::geography,
                 ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $4)
ORDER BY dist_m
LIMIT 1;

-- name: UpsertSpotAmenity :exec
INSERT INTO t_spot_amenities (spot_id, type, distance_m, source, count)
VALUES ($1, $2, $3, 'osm', 1)
ON CONFLICT (spot_id, type) DO UPDATE SET
    count = t_spot_amenities.count + 1,
    distance_m = LEAST(t_spot_amenities.distance_m, EXCLUDED.distance_m);

-- name: ClearPackAmenities :exec
DELETE FROM t_spot_amenities a
USING t_spots s
WHERE s.id = a.spot_id AND s.city_pack_id = $1 AND a.source = 'osm';

-- name: ListSpotsMissingAllSources :many
-- Active spots of the pack whose sources are all missing this run.
SELECT DISTINCT ss.spot_id
FROM t_spot_sources ss
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE sp.city_pack_id = $1 AND sp.status = 'active'
  AND ss.missing_runs >= $2
  AND NOT EXISTS (
      SELECT 1 FROM t_spot_sources s2
      JOIN t_ingest_features f
        ON f.pack_id = $1 AND f.source = s2.source AND f.source_id = s2.source_id
       AND f.role IN ('candidate','amenity')
      WHERE s2.spot_id = ss.spot_id);

-- name: ListPendingVanished :many
SELECT DISTINCT ss.spot_id
FROM t_spot_sources ss
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE sp.city_pack_id = $1 AND sp.status = 'pending'
  AND ss.missing_runs >= $2
  AND NOT EXISTS (
      SELECT 1 FROM t_spot_sources s2
      JOIN t_ingest_features f
        ON f.pack_id = $1 AND f.source = s2.source AND f.source_id = s2.source_id
       AND f.role IN ('candidate','amenity')
      WHERE s2.spot_id = ss.spot_id);

-- name: MarkExtractFetched :exec
UPDATE t_city_packs SET extract_fetched_at = $2
WHERE osm_extract = $1;

-- name: ListPacksByExtract :many
SELECT id, slug FROM t_city_packs WHERE osm_extract = $1;

-- name: ListDistinctExtracts :many
SELECT DISTINCT osm_extract FROM t_city_packs
WHERE osm_extract IS NOT NULL AND osm_extract <> ''
ORDER BY osm_extract;

-- name: TouchPackImport :exec
UPDATE t_city_packs SET last_import_at = now() WHERE id = $1;

-- name: NotifyIngestDone :exec
-- Transactional wake-up: delivered only if the ingest tx commits.
SELECT pg_notify('pickhour_ingest', '');
