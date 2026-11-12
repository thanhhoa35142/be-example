-- t_spot_sources gains provenance semantics:
--   via='primary'  — the candidate that IS the spot (park/beach/site or a
--                    table-cluster member); only primary sources keep a
--                    spot alive in the missing-source logic.
--   via='absorbed' — child candidates (site/table) absorbed into a parent
--                    park/beach; evidence links that never gate closing.
-- content_hash stores the candidate's content hash so SourceChanged works
-- on the osmium path (OSM element version is unavailable in geojsonseq).
ALTER TABLE t_spot_sources
    ADD COLUMN via          text        NOT NULL DEFAULT 'primary',
    ADD COLUMN content_hash text,
    ADD CONSTRAINT ck_t_spot_sources_via CHECK (via IN ('primary','absorbed'));
