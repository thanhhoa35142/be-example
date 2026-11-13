-- name: ListIngestPacks :many
SELECT id, slug, name, country, tz, osm_extract, boundary,
       last_import_at, extract_fetched_at
FROM t_city_packs
WHERE status IN ('beta','live')
ORDER BY slug;

-- name: PackBBox :one
SELECT ST_XMin(boundary), ST_YMin(boundary), ST_XMax(boundary), ST_YMax(boundary)
FROM t_city_packs WHERE id = $1;

-- name: ClearIngestFeatures :exec
DELETE FROM t_ingest_features WHERE pack_id = $1;

-- name: CopyIngestStaging :copyfrom
-- Raw layer bulk load (COPY). Rows are merged into t_ingest_features by
-- MergeIngestStaging inside the same tx.
INSERT INTO t_ingest_staging
    (pack_id, source, source_id, role, kind, amenity, quality,
     exclude_reason, version, tags, geojson, content_hash, run_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: MergeIngestStaging :execrows
-- Parses staged rows set-based and keeps ONE row per (source, source_id):
-- osmium exports a closed way both as LineString and as (Multi)Polygon —
-- the higher-dimension geometry wins.
INSERT INTO t_ingest_features
    (pack_id, source, source_id, role, kind, amenity, quality,
     exclude_reason, version, tags, geom, content_hash, run_id)
SELECT DISTINCT ON (s.source, s.source_id)
       s.pack_id, s.source, s.source_id, s.role, s.kind, s.amenity, s.quality,
       s.exclude_reason, s.version, s.tags::jsonb, s.g, s.content_hash, s.run_id
FROM (SELECT st.*, ST_GeomFromGeoJSON(st.geojson) AS g
      FROM t_ingest_staging st WHERE st.pack_id = $1) s
ORDER BY s.source, s.source_id, ST_Dimension(s.g) DESC;

-- name: ClearIngestStaging :exec
DELETE FROM t_ingest_staging WHERE pack_id = $1;

-- name: ListCandidateFeatures :many
SELECT source, source_id, kind, quality, version, tags, content_hash,
       ST_AsGeoJSON(geom) AS geojson,
       -- Anchor: ST_PointOnSurface stays inside concave areas (beach
       -- strips, U-shaped parks) where ST_Centroid can fall outside.
       ST_X(ST_PointOnSurface(geom)) AS lon,
       ST_Y(ST_PointOnSurface(geom)) AS lat,
       CASE WHEN ST_Dimension(geom) >= 2 THEN ST_AsGeoJSON(geom) END AS polygon_geojson,
       CASE WHEN ST_Dimension(geom) >= 2
            THEN ST_Area(geom::geography) / 10000 ELSE 0 END::float8 AS area_ha
FROM t_ingest_features
WHERE pack_id = $1 AND role = 'candidate'
ORDER BY source_id;

-- name: ListCandidateAmenityTypes :many
-- Quality input: distinct picnic amenity types inside (or within ~50 m of)
-- each candidate. Geometry-space prefilter on the raw GiST index.
SELECT c.source, c.source_id, count(DISTINCT a.amenity)::int AS types
FROM t_ingest_features c
JOIN t_ingest_features a
  ON a.pack_id = c.pack_id AND a.role = 'amenity'
 AND a.amenity IN ('toilet','water','bbq','playground','shelter')
 AND ST_DWithin(c.geom, a.geom, 0.0005)
WHERE c.pack_id = $1 AND c.role = 'candidate'
GROUP BY c.source, c.source_id;

-- name: ListPackSourceLinks :many
-- Planner preload (replaces one FindSpotBySource round-trip per candidate):
-- every source linked to this pack's spots, plus links of this run's
-- candidates owned by OTHER packs (boundary straddlers — first owner wins,
-- the API serves spots by bbox so one global spot covers both areas).
SELECT ss.source, ss.source_id, ss.spot_id, ss.version, ss.via,
       ss.content_hash, sp.city_pack_id
FROM t_spot_sources ss
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE sp.city_pack_id = $1
UNION ALL
SELECT ss.source, ss.source_id, ss.spot_id, ss.version, ss.via,
       ss.content_hash, sp.city_pack_id
FROM t_ingest_features f
JOIN t_spot_sources ss ON ss.source = f.source AND ss.source_id = f.source_id
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE f.pack_id = $1 AND f.role = 'candidate' AND sp.city_pack_id <> $1;

-- name: ListAbsorbedCandidates :many
-- Child candidates (site/table) whose centroid falls inside a park/beach
-- candidate polygon — they become evidence/sources of the parent, not spots.
SELECT c.source, c.source_id,
       p.source AS parent_source, p.source_id AS parent_source_id
FROM t_ingest_features c
JOIN t_ingest_features p
  ON p.pack_id = c.pack_id AND p.role = 'candidate'
 AND p.kind IN ('park','beach')
 AND p.geom && ST_Centroid(c.geom)
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
            AND p.geom && ST_Centroid(f.geom)
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

-- name: ListSpotsNearPoint :many
-- Spatial fallback candidates for OSM remaps: same columns as
-- ListPackSpots, restricted to reviewable statuses (closed/hidden/merged
-- must never be resurrected by a name match) within radius meters of the
-- point, nearest first, capped at 10.
SELECT id, status, quality, curated, merged_into, kind, name, names,
       attrs, country, tz, cell_id, status_reason,
       ST_X(geom::geometry) AS lon, ST_Y(geom::geometry) AS lat,
       CASE WHEN area IS NOT NULL THEN ST_AsGeoJSON(area) END AS area_geojson
FROM t_spots
WHERE city_pack_id = $1
  AND status IN ('pending','active')
  AND ST_DWithin(geom,
      ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4)
ORDER BY ST_Distance(geom,
      ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
LIMIT 10;

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
                THEN ST_Multi(ST_GeomFromGeoJSON(sqlc.narg('area'))) ELSE NULL END,
    -- cell follows the anchor (weather/crowd lookups are by cell)
    cell_id = sqlc.arg('cell_id'),
    updated_at = now()
WHERE id = $1;

-- name: SetSpotStatus :exec
UPDATE t_spots SET status = $2, status_reason = $3, updated_at = now()
WHERE id = $1;

-- name: UpsertSpotSources :exec
-- Batched provenance upsert (one statement per run instead of per source).
-- Caller guarantees (source, source_id) uniqueness within the batch.
INSERT INTO t_spot_sources (spot_id, source, source_id, version, via,
                            content_hash, imported_at, last_seen_at,
                            missing_runs)
SELECT u.spot_id, u.source, u.source_id, NULLIF(u.version, 0), u.via,
       NULLIF(u.content_hash, ''), now(), now(), 0
FROM (SELECT unnest(@spot_ids::uuid[])       AS spot_id,
             unnest(@sources::text[])        AS source,
             unnest(@source_ids::text[])     AS source_id,
             unnest(@versions::int[])        AS version,
             unnest(@vias::text[])           AS via,
             unnest(@content_hashes::text[]) AS content_hash) u
ON CONFLICT (source, source_id) DO UPDATE SET
    spot_id = EXCLUDED.spot_id,
    version = EXCLUDED.version,
    via = EXCLUDED.via,
    content_hash = COALESCE(EXCLUDED.content_hash, t_spot_sources.content_hash),
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

-- name: AssignPackAmenities :execrows
-- Set-based amenity attachment: every amenity feature goes to the nearest
-- active/pending spot footprint (area polygon, or anchor point for
-- point-only spots). Index-friendly: geometry-space prefilter/KNN on
-- ix_t_spots_footprint_gist (@radius_deg = radius widened by 1/cos(lat)),
-- exact geography distance only for the single winner per amenity.
-- Manual (non-osm) rows already present win on conflict.
INSERT INTO t_spot_amenities (spot_id, type, distance_m, source, count)
SELECT m.spot_id, m.amenity,
       LEAST(round(min(m.dist)), 32767)::smallint, 'osm',
       LEAST(count(*), 32767)::smallint
FROM (
    SELECT a.amenity, n.id AS spot_id,
           CASE WHEN ST_Intersects(n.fp, a.g) THEN 0
                ELSE ST_Distance(n.fp::geography, a.g::geography) END AS dist
    FROM (SELECT f.amenity, ST_PointOnSurface(f.geom) AS g
          FROM t_ingest_features f
          WHERE f.pack_id = @pack_id AND f.role = 'amenity'
            AND f.amenity IS NOT NULL) a
    CROSS JOIN LATERAL (
        SELECT s.id, COALESCE(s.area, s.geom::geometry) AS fp
        FROM t_spots s
        WHERE s.city_pack_id = @pack_id AND s.status IN ('active','pending')
          AND ST_DWithin(COALESCE(s.area, s.geom::geometry), a.g, @radius_deg::float8)
        ORDER BY COALESCE(s.area, s.geom::geometry) <-> a.g
        LIMIT 1) n
) m
WHERE m.dist <= @radius_m::float8
GROUP BY m.spot_id, m.amenity
ON CONFLICT (spot_id, type) DO NOTHING;

-- name: ClearPackAmenities :exec
DELETE FROM t_spot_amenities a
USING t_spots s
WHERE s.id = a.spot_id AND s.city_pack_id = $1 AND a.source = 'osm';

-- name: ListSpotsMissingAllSources :many
-- Active spots of the pack whose PRIMARY sources are all missing this run.
-- Absorbed child links (via='absorbed') never keep a spot alive.
SELECT DISTINCT ss.spot_id
FROM t_spot_sources ss
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE sp.city_pack_id = $1 AND sp.status = 'active'
  AND ss.via = 'primary'
  AND ss.missing_runs >= $2
  AND NOT EXISTS (
      SELECT 1 FROM t_spot_sources s2
      JOIN t_ingest_features f
        ON f.pack_id = $1 AND f.source = s2.source AND f.source_id = s2.source_id
       AND f.role IN ('candidate','amenity')
      WHERE s2.spot_id = ss.spot_id AND s2.via = 'primary');

-- name: ListPendingVanished :many
SELECT DISTINCT ss.spot_id
FROM t_spot_sources ss
JOIN t_spots sp ON sp.id = ss.spot_id
WHERE sp.city_pack_id = $1 AND sp.status = 'pending'
  AND ss.via = 'primary'
  AND ss.missing_runs >= $2
  AND NOT EXISTS (
      SELECT 1 FROM t_spot_sources s2
      JOIN t_ingest_features f
        ON f.pack_id = $1 AND f.source = s2.source AND f.source_id = s2.source_id
       AND f.role IN ('candidate','amenity')
      WHERE s2.spot_id = ss.spot_id AND s2.via = 'primary');

-- name: ListSourcelessSpots :many
-- Active/pending spots with no primary source left — their identity moved
-- to another spot (e.g. a site absorbed into a park). Close them out.
SELECT sp.id, sp.status
FROM t_spots sp
WHERE sp.city_pack_id = $1 AND sp.status IN ('active','pending')
  AND NOT sp.curated
  AND NOT EXISTS (
      SELECT 1 FROM t_spot_sources ss
      WHERE ss.spot_id = sp.id AND ss.via = 'primary');

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
