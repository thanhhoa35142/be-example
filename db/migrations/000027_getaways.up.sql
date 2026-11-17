-- Getaways (docs/GETAWAYS_SPEC.md §3.3): hourly city weather series, one row
-- per (pack, hour). Windows and day scores are computed at read time from
-- hours >= now, so a half-finished day never returns a window that passed.
--
-- weather_score is the median over the pack's active spots of
-- LEAST(s_weather, cap value of cap_reason), caps read from the active
-- t_scoring_configs row (not the total score, which mixes in crowd and
-- amenities). Only start hours 08:00-19:00 local are stored.
CREATE TABLE t_city_hour_outlook (
    pack_id         uuid        NOT NULL,
    ts_hour         timestamptz NOT NULL,
    local_date      date        NOT NULL,   -- (ts_hour AT TIME ZONE pack.tz)::date
    weather_score   smallint    NOT NULL,
    rain_share      real        NOT NULL,
    spots_n         integer     NOT NULL,   -- spots that had a row this hour
    top_spot_id     uuid,                   -- highest total score this hour
    top_spot_score  smallint,
    config_version  smallint    NOT NULL,
    computed_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_city_hour_outlook PRIMARY KEY (pack_id, ts_hour),
    CONSTRAINT fk_t_city_hour_outlook_t_city_packs FOREIGN KEY (pack_id) REFERENCES t_city_packs (id) ON DELETE CASCADE,
    CONSTRAINT fk_t_city_hour_outlook_t_spots FOREIGN KEY (top_spot_id) REFERENCES t_spots (id) ON DELETE SET NULL
);
CREATE INDEX ix_t_city_hour_outlook_date ON t_city_hour_outlook (local_date, pack_id);

-- The worker runs getaways.refresh right after scoring.recompute writes, once
-- at start, and on this interval as a safety net (offset 330 s: after the
-- scoring job's 4 min start delay).
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds)
VALUES ('getaways.refresh', 21600, true, 330)
ON CONFLICT (job) DO NOTHING;
