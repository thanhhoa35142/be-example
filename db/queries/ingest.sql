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
    name = $2, names = $3::jsonb,
    -- Only the tag-derived keys are replaced; derived env facts
    -- (waterDistanceM, treeCoverPct, protected, ...) and any non-ingest
    -- keys survive until the env step recomputes them.
    attrs = (attrs - sqlc.arg('owned_keys')::text[]) || sqlc.arg('attrs')::jsonb,
    quality = $4, geom = ST_SetSRID(ST_MakePoint($5, $6), 4326)::geography,
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
    -- Osmium features carry no OSM version (NULLIF(0,0) -> NULL); keep the
    -- last known version instead of erasing Overpass-sourced provenance.
    version = COALESCE(EXCLUDED.version, t_spot_sources.version),
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

-- ---------------------------------------------------------------------------
-- Environment layers (INGEST_ENV_FACTS_PLAN). Layer rows live in
-- t_ingest_features as role='excluded' with exclude_reason layer_water /
-- layer_wood / layer_campsite / layer_protected (no schema change: the role
-- CHECK only allows candidate|amenity|excluded). Water rows carry their kind
-- (sea|river|canal|lake|pond) in `kind`.
-- ---------------------------------------------------------------------------

-- name: ValidateLayerGeoms :execrows
-- One GEOS-validity pass over this pack's layer rows so the set operations
-- below never raise TopologyException (one bad OSM polygon would otherwise
-- fail the whole pack transaction).
UPDATE t_ingest_features
SET geom = ST_CollectionExtract(ST_MakeValid(geom),
                                CASE WHEN ST_Dimension(geom) = 2 THEN 3
                                     WHEN ST_Dimension(geom) = 1 THEN 2 ELSE 1 END)
WHERE pack_id = $1 AND role = 'excluded' AND exclude_reason LIKE 'layer\_%'
  AND NOT ST_IsValid(geom);

-- name: AnnotateWaterLayers :execrows
-- Classifies each water row once, so the per-spot queries only compare:
--   tags.areaM2  polygon area in m2 (absent for lines)
--   tags.major   true for a real waterfront: the sea; a river (line or
--                riverbank polygon); a lake polygon of at least @lake_min_m2
--                (5 ha); a canal that is a polygon of at least @canal_min_m2
--                (2 ha) or a line mapped with width >= @canal_width_m (20 m).
-- Standing water below the lake limit becomes kind 'pond' (recorded, never
-- waterside); a canal that is not major keeps its kind but is not major.
UPDATE t_ingest_features f
SET kind = CASE WHEN f.kind = 'lake' AND a.m2 IS NOT NULL
                     AND a.m2 < sqlc.arg('lake_min_m2')::float8 THEN 'pond' ELSE f.kind END,
    tags = f.tags || jsonb_build_object(
        'areaM2', round(a.m2),
        'major', CASE
            WHEN f.kind IN ('sea', 'river') THEN true
            WHEN f.kind = 'lake' THEN a.m2 IS NULL OR a.m2 >= sqlc.arg('lake_min_m2')::float8
            WHEN f.kind = 'canal' THEN
                 CASE WHEN a.m2 IS NOT NULL THEN a.m2 >= sqlc.arg('canal_min_m2')::float8
                      ELSE COALESCE(substring(f.tags->>'width' FROM '^[0-9]+(?:\.[0-9]+)?')::float8, 0)
                           >= sqlc.arg('canal_width_m')::float8 END
            ELSE false END)
FROM (SELECT source, source_id,
             CASE WHEN ST_Dimension(geom) = 2 THEN ST_Area(geom::geography) END AS m2
      FROM t_ingest_features
      WHERE pack_id = @pack_id AND exclude_reason = 'layer_water') a
WHERE f.pack_id = @pack_id AND f.source = a.source AND f.source_id = a.source_id
  AND f.exclude_reason = 'layer_water';

-- name: AnnotateProtectedLayers :execrows
-- tags.areaM2 of each protected polygon: the smallest containing area wins.
UPDATE t_ingest_features
SET tags = tags || jsonb_build_object('areaM2', round(ST_Area(geom::geography)))
WHERE pack_id = $1 AND exclude_reason = 'layer_protected' AND ST_Dimension(geom) = 2;

-- name: SubdivideLayers :execrows
-- Splits every large water/wood/protected geometry (over @max_vertices
-- points) into pieces of at most @piece_vertices points, stored as extra
-- layer rows (source_id '<id>#<n>', same tags) in place of the original.
-- Each piece is small and GiST-indexed, so distance, containment and clipping
-- touch only the few pieces near a spot instead of a whole forest, lake or
-- coastline (ST_Union of full forests exceeded the statement timeout).
WITH big AS (
    SELECT t.* FROM t_ingest_features t
    WHERE t.pack_id = @pack_id
      AND t.exclude_reason IN ('layer_water', 'layer_wood', 'layer_protected')
      AND ST_NPoints(t.geom) > @max_vertices::int
), ins AS (
    INSERT INTO t_ingest_features
        (pack_id, source, source_id, role, kind, amenity, quality, exclude_reason,
         version, tags, geom, content_hash, run_id)
    SELECT b.pack_id, b.source, b.source_id || '#' || d.n, b.role, b.kind, b.amenity,
           b.quality, b.exclude_reason, b.version, b.tags, d.g, b.content_hash, b.run_id
    FROM big b
    CROSS JOIN LATERAL ST_Subdivide(b.geom, @piece_vertices::int) WITH ORDINALITY AS d(g, n)
    RETURNING 1
)
DELETE FROM t_ingest_features f
USING big
WHERE f.pack_id = big.pack_id AND f.source = big.source AND f.source_id = big.source_id;

-- name: ExcludeCampsiteCandidates :many
-- Candidates that sit inside a camp site / caravan site are not picnic
-- spots (typically a tourism=picnic_site mapped inside a campsite). They
-- are turned into excluded rows BEFORE the planner loads candidates, so
-- they never get a spot and never keep an old one alive. Containment is
-- tested on the candidate's point-on-surface, so a park that merely
-- touches a campsite is kept; parks/beaches are dropped only when the
-- campsite covers at least @park_cover (e.g. 0.5) of their area.
-- Camp site nodes (no area) only exclude site/table candidates within
-- @node_radius_m.
WITH camp AS MATERIALIZED (
    SELECT k.geom, ST_Dimension(k.geom) AS dim
    FROM t_ingest_features k
    WHERE k.pack_id = @pack_id AND k.exclude_reason = 'layer_campsite'
), hit AS (
    -- area campsites: the candidate's bbox must overlap (index probe on
    -- c.geom), then its anchor must be inside
    SELECT c.source, c.source_id
    FROM camp k
    JOIN t_ingest_features c
      ON c.pack_id = @pack_id AND c.role = 'candidate' AND c.geom && k.geom
    WHERE k.dim = 2
      AND ST_Contains(k.geom, ST_PointOnSurface(c.geom))
      AND CASE WHEN c.kind IN ('site','table') THEN true
               WHEN NOT ST_IsValid(c.geom) THEN false
               ELSE ST_Area(ST_Intersection(k.geom, c.geom)) / NULLIF(ST_Area(c.geom), 0)
                    >= sqlc.arg('park_cover')::float8 END
    UNION
    -- camp site nodes
    SELECT c.source, c.source_id
    FROM camp k
    JOIN t_ingest_features c
      ON c.pack_id = @pack_id AND c.role = 'candidate' AND c.kind IN ('site','table')
     AND c.geom && ST_Expand(k.geom, @node_radius_deg::float8)
    WHERE k.dim = 0
      AND ST_Distance(k.geom::geography, ST_PointOnSurface(c.geom)::geography) <= @node_radius_m::float8
)
UPDATE t_ingest_features f
SET role = 'excluded', exclude_reason = 'inside_campsite'
FROM hit
WHERE f.pack_id = @pack_id AND f.source = hit.source AND f.source_id = hit.source_id
RETURNING f.source, f.source_id;

-- name: UpdatePackWaterAttrs :execrows
-- Per-spot water facts into t_spots.attrs (merge of the water keys only):
--   waterDistanceM int   meters from the spot footprint (area, else point)
--                        to the nearest water, only when <= @max_m
--   waterKind text       sea|river|canal|lake|pond of that water
--   waterBodyHa float    area of the water body when it is a polygon
--   waterside bool       true when that water is a real waterfront (see
--                        AnnotateWaterLayers: tags.major) within @waterside_m
-- The nearest major water wins; non-major water (small lakes = ponds, narrow
-- canals) is reported only when no major water is in range, ponds last.
--
-- Cost model (a dense NL pack has ~250 water pieces within 500 m of EVERY
-- spot): the exact geography distance is the expensive part (a sphere
-- computation per pair, no index), so it is evaluated only for the two
-- planar-nearest pieces of each class (major / other / pond) per spot, not
-- for every candidate. Candidates come from the GiST index on the expanded
-- spot box; their planar distance in degrees is cheap. The spot footprint is
-- simplified to about 3 m for the candidate search; the exact distance of the
-- winners uses the same simplified footprint (error below 3 m against a 50 m
-- threshold).
-- Every stage is MATERIALIZED: left inlined, the planner (which estimates one
-- row for these CTEs) re-ran the window per spot and evaluated the geography
-- distance inside nested-loop join filters (5 s for 100 spots on a giant lake).
-- Only active/pending spots; curated spots included (only env keys change).
WITH sp AS (
    SELECT s.id, g.fp, g.fp::geography AS fpg, ST_Expand(g.fp, @radius_deg::float8) AS box
    FROM t_spots s
    CROSS JOIN LATERAL (
        SELECT ST_SimplifyPreserveTopology(COALESCE(s.area, s.geom::geometry), 0.00003) AS fp) g
    WHERE s.city_pack_id = @pack_id AND s.status IN ('active','pending')
), cand AS MATERIALIZED (
    SELECT sp.id, f.source, f.source_id, f.kind,
           COALESCE((f.tags->>'major')::bool, false) AS major,
           (f.tags->>'areaM2')::float8 AS m2,
           ST_Distance(f.geom, sp.fp) AS dd -- geometry is not carried: keeps the window sort narrow
    FROM sp
    JOIN t_ingest_features f
      ON f.pack_id = @pack_id AND f.exclude_reason = 'layer_water' AND f.geom && sp.box
), ranked AS MATERIALIZED (
    SELECT c.*,
           CASE WHEN c.major THEN 0 WHEN c.kind <> 'pond' THEN 1 ELSE 2 END AS cls,
           row_number() OVER (
               PARTITION BY c.id,
                            CASE WHEN c.major THEN 0 WHEN c.kind <> 'pond' THEN 1 ELSE 2 END
               ORDER BY c.dd) AS rn
    FROM cand c
    WHERE c.dd <= @radius_deg::float8
), exact AS MATERIALIZED (
    SELECT r.id, r.kind, r.major, r.m2, r.cls,
           CASE WHEN r.dd = 0 THEN 0::float8
                ELSE ST_Distance(sp.fpg, f.geom::geography) END AS dist
    FROM ranked r
    JOIN sp ON sp.id = r.id
    JOIN t_ingest_features f ON f.pack_id = @pack_id AND f.source = r.source AND f.source_id = r.source_id
    WHERE r.rn <= 2
), best AS MATERIALIZED (
    SELECT DISTINCT ON (e.id) e.id, e.kind, e.major, e.m2, e.dist
    FROM exact e
    WHERE e.dist <= @max_m::float8
    ORDER BY e.id, e.cls, e.dist
), calc AS MATERIALIZED (
    SELECT b.id,
           jsonb_strip_nulls(jsonb_build_object(
               'waterDistanceM', round(b.dist)::int,
               'waterKind', b.kind,
               'waterBodyHa', round((b.m2 / 10000.0)::numeric, 1),
               'waterside', CASE WHEN b.major AND b.dist <= @waterside_m::float8 THEN true END)) AS env
    FROM best b
)
UPDATE t_spots s
SET attrs = (s.attrs - @water_keys::text[]) || COALESCE(calc.env, '{}'::jsonb)
FROM sp
LEFT JOIN calc ON calc.id = sp.id
WHERE sp.id = s.id
  AND s.attrs IS DISTINCT FROM ((s.attrs - @water_keys::text[]) || COALESCE(calc.env, '{}'::jsonb));

-- name: UpdatePackTreeAttrs :execrows
-- treeCoverPct int: 0-100 share of the spot AREA under wood/forest polygons
-- (null for point-only spots). Only the layer pieces that intersect the spot
-- are clipped (pieces fully inside are taken whole) and the small clipped
-- pieces are unioned, so overlapping forests count once.
WITH sp AS (
    SELECT s.id,
           CASE WHEN ST_IsValid(s.area) THEN s.area ELSE ST_MakeValid(s.area) END AS a
    FROM t_spots s
    WHERE s.city_pack_id = @pack_id AND s.status IN ('active','pending') AND s.area IS NOT NULL
), calc AS (
    SELECT sp.id,
           jsonb_build_object('treeCoverPct', COALESCE(LEAST(100, GREATEST(0, round(
               100 * ST_Area(u.g::geography) / NULLIF(ST_Area(sp.a::geography), 0)))), 0)::int) AS env
    FROM sp
    LEFT JOIN LATERAL (
        SELECT ST_Union(CASE WHEN ST_Covers(sp.a, f.geom) THEN f.geom
                             ELSE ST_CollectionExtract(ST_Intersection(sp.a, f.geom), 3) END) AS g
        FROM t_ingest_features f
        WHERE f.pack_id = @pack_id AND f.geom && sp.a AND ST_Dimension(f.geom) = 2
          AND (f.exclude_reason = 'layer_wood'
               OR (f.role = 'candidate' AND (f.tags->>'natural' = 'wood' OR f.tags->>'landuse' = 'forest')))
          AND ST_Intersects(f.geom, sp.a)) u ON true
)
UPDATE t_spots s
SET attrs = (s.attrs - 'treeCoverPct'::text) || calc.env
FROM calc
WHERE calc.id = s.id AND s.attrs IS DISTINCT FROM ((s.attrs - 'treeCoverPct'::text) || calc.env);

-- name: UpdatePackProtectedAttrs :execrows
--   protected bool       the spot's own source is a protected area/nature
--                        reserve, or its anchor lies inside one
--   protectedFee text    yes|no from the smallest containing protected area
--   protectedOperator    operator of that area
WITH sp AS (
    SELECT s.id, s.geom::geometry AS pt
    FROM t_spots s
    WHERE s.city_pack_id = @pack_id AND s.status IN ('active','pending')
), calc AS (
    SELECT sp.id,
           jsonb_strip_nulls(jsonb_build_object(
               'protected', CASE WHEN own.n > 0 OR pa.hit THEN true END,
               'protectedFee', CASE WHEN pa.fee IN ('yes','no') THEN pa.fee END,
               'protectedOperator', pa.operator)) AS env
    FROM sp
    CROSS JOIN LATERAL (
        SELECT count(*) AS n
        FROM t_spot_sources ss
        JOIN t_ingest_features f
          ON f.pack_id = @pack_id AND f.source = ss.source AND f.source_id = ss.source_id
        WHERE ss.spot_id = sp.id AND ss.via = 'primary'
          AND (f.tags->>'boundary' = 'protected_area' OR f.tags->>'leisure' = 'nature_reserve')) own
    LEFT JOIN LATERAL (
        SELECT true AS hit, f.tags->>'fee' AS fee, f.tags->>'operator' AS operator
        FROM t_ingest_features f
        WHERE f.pack_id = @pack_id AND f.exclude_reason = 'layer_protected'
          AND ST_Dimension(f.geom) = 2 AND f.geom && sp.pt AND ST_Intersects(f.geom, sp.pt)
        ORDER BY (f.tags->>'areaM2')::float8 NULLS LAST
        LIMIT 1) pa ON true
)
UPDATE t_spots s
SET attrs = (s.attrs - @protected_keys::text[]) || calc.env
FROM calc
WHERE calc.id = s.id AND s.attrs IS DISTINCT FROM ((s.attrs - @protected_keys::text[]) || calc.env);

-- name: EnvStepSavepoint :exec
SAVEPOINT env_step;

-- name: EnvStepRollback :exec
ROLLBACK TO SAVEPOINT env_step;

-- name: EnvStepRelease :exec
RELEASE SAVEPOINT env_step;

-- name: GetStatementTimeout :one
SELECT current_setting('statement_timeout')::text;

-- name: SetLocalStatementTimeout :exec
SELECT set_config('statement_timeout', @timeout::text, true);

-- name: PackEnvStats :one
SELECT
    count(*) FILTER (WHERE attrs ? 'waterDistanceM')::int AS water_near,
    count(*) FILTER (WHERE attrs->>'waterside' = 'true')::int AS waterside,
    count(*) FILTER (WHERE attrs ? 'treeCoverPct')::int AS with_tree_cover,
    count(*) FILTER (WHERE attrs->>'protected' = 'true')::int AS protected
FROM t_spots
WHERE city_pack_id = $1 AND status IN ('active','pending');

-- name: AnalyzeIngestFeatures :exec
-- Rows merged inside the current tx have no planner statistics yet; with the
-- environment layers the table holds ~10x more rows per pack and the
-- candidate/absorption joins pick seq-scan plans without this.
ANALYZE t_ingest_features;
