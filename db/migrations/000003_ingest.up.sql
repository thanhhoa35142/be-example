-- ============================================================================
-- 000003: ingest pipeline deltas (docs/INGEST_STRATEGY.md §6)
-- Raw feature layer + provenance/diff columns + job schedules.
-- ============================================================================

-- Raw OSM snapshot per city pack: every classified feature kept for audit
-- and re-planning without re-downloading the extract.
CREATE TABLE t_ingest_features (
    pack_id        uuid        NOT NULL,
    source         text        NOT NULL,
    source_id      text        NOT NULL,
    role           text        NOT NULL,
    kind           text,
    amenity        text,
    quality        smallint,
    exclude_reason text,
    version        integer,
    tags           jsonb       DEFAULT '{}'::jsonb NOT NULL,
    geom           geometry(Geometry, 4326),
    content_hash   text        NOT NULL,
    run_id         bigint,
    seen_at        timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT pk_t_ingest_features            PRIMARY KEY (pack_id, source, source_id),
    CONSTRAINT fk_t_ingest_features_t_city_packs FOREIGN KEY (pack_id) REFERENCES t_city_packs (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_ingest_features_t_job_runs  FOREIGN KEY (run_id) REFERENCES t_job_runs (id),
    CONSTRAINT ck_t_ingest_features_role        CHECK (role IN ('candidate','amenity','excluded')),
    CONSTRAINT ck_t_ingest_features_quality     CHECK (quality IS NULL OR quality BETWEEN 0 AND 100)
);
CREATE INDEX ix_t_ingest_features_geom ON t_ingest_features USING gist (geom);

-- Provenance diffing: when each source was last seen and how many
-- consecutive runs it has been missing (drives close decisions).
ALTER TABLE t_spot_sources
    ADD COLUMN last_seen_at timestamptz,
    ADD COLUMN missing_runs smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT ck_t_spot_sources_missing_runs CHECK (missing_runs >= 0);

-- Spot quality score (0-100, ingest-assigned), free-form attrs from the
-- source tags, and admin curation lock (importer never touches curated
-- or hidden/merged rows).
ALTER TABLE t_spots
    ADD COLUMN quality          smallint,
    ADD COLUMN attrs            jsonb       DEFAULT '{}'::jsonb NOT NULL,
    ADD COLUMN curated          boolean     NOT NULL DEFAULT false,
    ADD COLUMN status_reason    text,
    ADD COLUMN status_changed_at timestamptz,
    ADD CONSTRAINT ck_t_spots_quality CHECK (quality IS NULL OR quality BETWEEN 0 AND 100);

-- Amenity rows are unique per (spot, type) — count preserves how many
-- source objects back each type (e.g. 4 toilets).
ALTER TABLE t_spot_amenities
    ADD COLUMN count smallint NOT NULL DEFAULT 1,
    ADD CONSTRAINT ck_t_spot_amenities_count CHECK (count >= 1);

ALTER TABLE t_job_runs
    ADD COLUMN stats jsonb;

ALTER TABLE t_city_packs
    ADD COLUMN extract_fetched_at timestamptz;

-- Keep status_changed_at in sync on every accepted transition.
CREATE OR REPLACE FUNCTION fn_t_spots_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[];
BEGIN
    allowed := CASE OLD.status
        WHEN 'pending' THEN ARRAY['active','hidden']
        WHEN 'active'  THEN ARRAY['closed','hidden','merged']
        WHEN 'closed'  THEN ARRAY['active','hidden','merged']
        WHEN 'hidden'  THEN ARRAY['active','merged']
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

-- Ingest jobs (see INGEST_STRATEGY.md §5). Schedules exist ahead of the
-- worker registry entries; ingest.osm no-ops until extracts arrive.
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds) VALUES
    ('ingest.osm',            86400, true, 300),
    ('ingest.extract.refresh', 604800, true, 660)
ON CONFLICT (job) DO NOTHING;
