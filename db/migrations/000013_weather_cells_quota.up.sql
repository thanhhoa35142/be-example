-- Weather cells move to a coarser H3 resolution (geo.WeatherResolution);
-- rows stored under the old per-spot resolution are dead weight — lookups
-- now map spot cells to weather parents and would never match them.
TRUNCATE weather_hourly;

-- Daily call budget per external provider. Workers consume quota
-- atomically before fan-out so a runaway job can't burn the provider's
-- daily allowance (e.g. Open-Meteo free tier ~10k calls/day).
CREATE TABLE provider_quota (
    provider  TEXT        NOT NULL,
    day       DATE        NOT NULL,
    used      INTEGER     NOT NULL DEFAULT 0 CHECK (used >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, day)
);
