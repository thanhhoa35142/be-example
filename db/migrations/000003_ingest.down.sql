DELETE FROM t_worker_job_schedules WHERE job IN ('ingest.osm', 'ingest.extract.refresh');

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
        PERFORM fn_log_transition('spot', NEW.id::text, OLD.status, NEW.status);
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE t_city_packs    DROP COLUMN extract_fetched_at;
ALTER TABLE t_job_runs      DROP COLUMN stats;
ALTER TABLE t_spot_amenities DROP COLUMN count;
ALTER TABLE t_spots
    DROP COLUMN quality,
    DROP COLUMN attrs,
    DROP COLUMN curated,
    DROP COLUMN status_reason,
    DROP COLUMN status_changed_at;
ALTER TABLE t_spot_sources
    DROP COLUMN last_seen_at,
    DROP COLUMN missing_runs;
DROP TABLE IF EXISTS t_ingest_features;
