-- Domain: reference data and job operations

CREATE TABLE holiday (
    country  CHAR(2) NOT NULL,
    region   TEXT NOT NULL DEFAULT '',
    date     DATE NOT NULL,
    kind     TEXT CHECK (kind IN ('public', 'school')),
    name     TEXT,
    PRIMARY KEY (country, region, date, kind)
);

CREATE TABLE job_run (
    id             BIGSERIAL PRIMARY KEY,
    job            TEXT NOT NULL,
    city_pack_id   UUID,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ,
    status         TEXT NOT NULL,
    rows_affected  INT,
    error          TEXT
);
