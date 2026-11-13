-- Schema-only rollback. The data cleanup (id normalization, duplicate merges)
-- is not reversible; restore from backup if needed.
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

DROP INDEX IF EXISTS ix_t_spots_footprint_gist;
DROP TABLE IF EXISTS t_ingest_staging;

ALTER TABLE t_ingest_features DROP CONSTRAINT fk_t_ingest_features_t_job_runs;
ALTER TABLE t_ingest_features
    ADD CONSTRAINT fk_t_ingest_features_t_job_runs
    FOREIGN KEY (run_id) REFERENCES t_job_runs (id);
