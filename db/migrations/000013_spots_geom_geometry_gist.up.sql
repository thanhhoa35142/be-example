-- Bbox list queries filter on s.geom::geometry && ST_MakeEnvelope(...).
-- geom is geography, so the cast defeats both existing gist indexes
-- (ix_t_spots_geom_gist is on the geography column; ix_t_spots_footprint_gist
-- is on COALESCE(area, geom::geometry)) — every /v1/spots?bbox= miss
-- seq-scanned all of t_spots. This expression index matches the query
-- exactly, with zero change to result semantics.
CREATE INDEX IF NOT EXISTS ix_t_spots_geom_geometry_gist
    ON t_spots USING gist ((geom::geometry));
