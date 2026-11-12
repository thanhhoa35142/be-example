-- Domain: core spatial data (city packs, spots, provenance, amenities)

CREATE TABLE city_pack (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    slug             TEXT UNIQUE NOT NULL,
    name             TEXT NOT NULL,
    country          CHAR(2) NOT NULL,
    tz               TEXT NOT NULL,
    boundary         geometry(MultiPolygon, 4326) NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('draft', 'beta', 'live', 'retired')),
    osm_extract      TEXT,
    last_import_at   TIMESTAMPTZ
);

CREATE TABLE spot (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    city_pack_id     UUID NOT NULL REFERENCES city_pack(id),
    kind             TEXT NOT NULL CHECK (kind IN ('table', 'site', 'park', 'beach')),
    name             TEXT,
    names            JSONB NOT NULL DEFAULT '{}',
    geom             geography(Point, 4326) NOT NULL,
    area             geometry(MultiPolygon, 4326),
    country          CHAR(2) NOT NULL,
    city             TEXT,
    tz               TEXT NOT NULL,
    cell_id          BIGINT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'closed', 'hidden', 'merged')),
    merged_into      UUID REFERENCES spot(id),
    last_verified_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX spot_geom_gist        ON spot USING GIST (geom);
CREATE INDEX spot_area_gist        ON spot USING GIST (area) WHERE area IS NOT NULL;
CREATE INDEX spot_city_status_idx  ON spot (city_pack_id, status);
CREATE INDEX spot_cell_id_idx      ON spot (cell_id);

CREATE TABLE spot_source (
    spot_id      UUID NOT NULL REFERENCES spot(id),
    source       TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    version      INT,
    imported_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (source, source_id)
);

CREATE TABLE spot_amenity (
    spot_id    UUID NOT NULL REFERENCES spot(id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    distance_m SMALLINT NOT NULL,
    source     TEXT NOT NULL,
    PRIMARY KEY (spot_id, type)
);
