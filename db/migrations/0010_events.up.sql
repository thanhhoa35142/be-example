-- Domain: product analytics events (privacy-safe: no location, props capped)

CREATE TABLE event (
    id         UUID NOT NULL DEFAULT uuidv7(),
    device_id  UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    props      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE event_default PARTITION OF event DEFAULT;

CREATE INDEX event_device_day_idx ON event (device_id, created_at);
CREATE INDEX event_name_day_idx   ON event (name, created_at);
