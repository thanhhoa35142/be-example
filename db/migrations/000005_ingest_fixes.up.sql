-- ============================================================================
-- 000005: ingest fixes (docs/INGEST_REVIEW_2026-11-13.md)
--   * osmium area ids "a<2n>"/"a<2n+1>" normalized to "w<n>"/"r<n>" and the
--     LineString/Polygon duplicates of closed ways collapsed
--   * COPY staging table for the raw layer
--   * footprint index for set-based amenity assignment
--   * run_id FK no longer blocks t_job_runs retention
--   * state machine: hidden → pending (importer re-evaluation),
--     pending → merged (duplicate cleanup)
-- ============================================================================

-- Retention purges t_job_runs; the raw layer only keeps run_id for audit.
ALTER TABLE t_ingest_features DROP CONSTRAINT fk_t_ingest_features_t_job_runs;
ALTER TABLE t_ingest_features
    ADD CONSTRAINT fk_t_ingest_features_t_job_runs
    FOREIGN KEY (run_id) REFERENCES t_job_runs (id) ON DELETE SET NULL;

-- COPY target for the raw layer. UNLOGGED: rows only live inside one ingest
-- tx (merged into t_ingest_features, then deleted), so WAL is pure waste.
-- Geometry/tags stay text here; the merge parses them set-based.
CREATE UNLOGGED TABLE t_ingest_staging (
    pack_id        uuid    NOT NULL,
    source         text    NOT NULL,
    source_id      text    NOT NULL,
    role           text    NOT NULL,
    kind           text,
    amenity        text,
    quality        smallint,
    exclude_reason text,
    version        integer,
    tags           text    NOT NULL,
    geojson        text    NOT NULL,
    content_hash   text    NOT NULL,
    run_id         bigint
);
CREATE INDEX ix_t_ingest_staging_pack ON t_ingest_staging (pack_id);

-- Amenity assignment matches against the polygon when present, the anchor
-- point otherwise (point-only sites/tables also receive amenities).
CREATE INDEX ix_t_spots_footprint_gist ON t_spots
    USING gist ((COALESCE(area, geom::geometry)));

CREATE OR REPLACE FUNCTION fn_t_spots_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'pending' THEN ARRAY['active','hidden','merged']
        WHEN 'active'  THEN ARRAY['closed','hidden','merged']
        WHEN 'closed'  THEN ARRAY['active','hidden','merged']
        WHEN 'hidden'  THEN ARRAY['active','pending','merged']
        ELSE ARRAY[]::text[]
    END;
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM fn_assert_transition(allowed, OLD.status, NEW.status);
        NEW.status_changed_at := now();
        PERFORM fn_log_transition('spot', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- Data cleanup: osmium area ids.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE tmp_area_ids ON COMMIT DROP AS
SELECT ss.source, ss.source_id AS a_id, ss.spot_id AS a_spot,
       CASE WHEN substr(ss.source_id, 2)::bigint % 2 = 0
            THEN 'w' || (substr(ss.source_id, 2)::bigint / 2)
            ELSE 'r' || ((substr(ss.source_id, 2)::bigint - 1) / 2) END AS norm_id
FROM t_spot_sources ss
WHERE ss.source = 'osm' AND ss.source_id ~ '^a[0-9]+$';

-- 1. Duplicate spots: the LineString twin created its own spot. Keep the
--    polygon spot (correct geometry), merge the twin into it.
CREATE TEMP TABLE tmp_dup_spots ON COMMIT DROP AS
SELECT DISTINCT ON (w.spot_id) w.spot_id AS twin, t.a_spot AS keep
FROM tmp_area_ids t
JOIN t_spot_sources w ON w.source = t.source AND w.source_id = t.norm_id
WHERE w.spot_id <> t.a_spot
ORDER BY w.spot_id, t.a_spot;

-- A twin may itself be the polygon spot of another pair; never merge a spot
-- that is also a keeper (avoids merge chains).
DELETE FROM tmp_dup_spots d WHERE EXISTS (SELECT 1 FROM tmp_dup_spots k WHERE k.keep = d.twin);

UPDATE t_spots sp
SET status = 'merged', merged_into = d.keep, status_reason = 'duplicate_osm_area',
    updated_at = now()
FROM tmp_dup_spots d
WHERE sp.id = d.twin AND sp.status IN ('pending','active','closed','hidden')
  AND NOT sp.curated;

-- User-owned references follow the keeper so alerts keep firing (the API
-- also redirects merged spots via merged_into). Derived rows (scores,
-- crowd estimates) are recomputed and cascade-safe.
UPDATE t_alert_rules r SET spot_id = d.keep
FROM tmp_dup_spots d, t_spots sp
WHERE r.spot_id = d.twin AND sp.id = d.twin AND sp.status = 'merged';

-- Re-point every source of a merged twin at its keeper.
UPDATE t_spot_sources ss SET spot_id = d.keep
FROM tmp_dup_spots d, t_spots sp
WHERE ss.spot_id = d.twin AND sp.id = d.twin AND sp.status = 'merged';

-- 2. Same-spot pairs: drop the area-id link when the normalized link exists.
DELETE FROM t_spot_sources ss
USING tmp_area_ids t
WHERE ss.source = t.source AND ss.source_id = t.a_id
  AND EXISTS (SELECT 1 FROM t_spot_sources n
              WHERE n.source = t.source AND n.source_id = t.norm_id);

-- 3. Remaining area ids: rename in place. The surviving normalized link keeps
--    its old (LineString) content_hash, so the next run sees a change and
--    restores area + quality from the polygon.
UPDATE t_spot_sources ss SET source_id = t.norm_id, content_hash = NULL
FROM tmp_area_ids t
WHERE ss.source = t.source AND ss.source_id = t.a_id;

UPDATE t_spot_sources ss SET content_hash = NULL
FROM tmp_area_ids t
WHERE ss.source = t.source AND ss.source_id = t.norm_id;

-- 4. Raw layer is a re-derivable snapshot keyed by the old ids — drop it;
--    the next ingest run rebuilds it (ClearRaw runs first anyway).
--    Amenities are NOT touched: double counts are rebuilt by the first run,
--    deleting them here would leave packs without amenities until the
--    freshness gate next opens.
TRUNCATE t_ingest_features;
