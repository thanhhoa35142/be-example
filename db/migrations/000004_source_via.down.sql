ALTER TABLE t_spot_sources
    DROP CONSTRAINT ck_t_spot_sources_via,
    DROP COLUMN via,
    DROP COLUMN content_hash;
