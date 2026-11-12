-- Seed data carried over from squashed migrations 7-11, renamed to t_*.
-- Order matters: t_crowd_curves references t_scoring_configs(version).

INSERT INTO t_scoring_configs (version, weights, thresholds, active_from)
VALUES (
    1,
    '{"weather":0.40,"crowd":0.25,"amenity":0.20,"shade":0.10,"fresh":0.05}'::jsonb,
    '{"rain_prob_threshold":60,"rain_score_cap":40,"heat_cap_c":35,"heat_score_cap":30,"cold_cap_c":-10,"cold_score_cap":30}'::jsonb,
    '2026-01-01'::timestamptz
)
ON CONFLICT (version) DO NOTHING;

-- crowd_curve config_version 1: hourly factors per spot kind.
-- Park-family (park/site/table) peaks weekday lunch + weekend afternoon;
-- beach peaks midday. Values are starting assumptions (spec §16).
INSERT INTO t_crowd_curves (kind, dow, hour, factor, config_version)
SELECT k.kind, d.dow, h.hour,
    CASE
        WHEN k.kind = 'beach' THEN CASE
            WHEN h.hour BETWEEN 0 AND 7 THEN 0.10
            WHEN h.hour BETWEEN 8 AND 10 THEN CASE WHEN d.dow IN (0, 6) THEN 0.60 ELSE 0.45 END
            WHEN h.hour BETWEEN 11 AND 15 THEN CASE WHEN d.dow IN (0, 6) THEN 1.15 ELSE 1.00 END
            WHEN h.hour BETWEEN 16 AND 18 THEN CASE WHEN d.dow IN (0, 6) THEN 0.95 ELSE 0.80 END
            WHEN h.hour BETWEEN 19 AND 21 THEN 0.40
            ELSE 0.15
        END
        ELSE CASE
            WHEN h.hour BETWEEN 0 AND 5 THEN 0.15
            WHEN h.hour BETWEEN 6 AND 8 THEN 0.35
            WHEN h.hour BETWEEN 9 AND 11 THEN CASE WHEN d.dow IN (0, 6) THEN 0.65 ELSE 0.45 END
            WHEN h.hour BETWEEN 12 AND 13 THEN CASE WHEN d.dow IN (0, 6) THEN 0.95 ELSE 0.85 END
            WHEN h.hour BETWEEN 14 AND 17 THEN CASE WHEN d.dow IN (0, 6) THEN 1.00 ELSE 0.70 END
            WHEN h.hour BETWEEN 18 AND 20 THEN 0.55
            ELSE 0.25
        END
    END,
    1
FROM (VALUES ('park'), ('beach'), ('site'), ('table')) AS k(kind)
CROSS JOIN generate_series(0, 6) AS d(dow)
CROSS JOIN generate_series(0, 23) AS h(hour)
ON CONFLICT DO NOTHING;

-- Plan limits (spec §15): Free gets 1 alert area, Plus unlimited (-1).
INSERT INTO t_plan_limits (plan, feature, limit_value) VALUES
    ('free', 'alert_rules', 1),
    ('plus', 'alert_rules', -1)
ON CONFLICT (plan, feature) DO NOTHING;

-- Worker job schedules. interval_seconds 60..2592000.
-- offset_seconds staggers first runs within the same tick window.
INSERT INTO t_worker_job_schedules (job, interval_seconds, enabled, offset_seconds) VALUES
    ('weather.refresh',    3600,  true,    0),
    ('crowd.refresh',     86400,  true,  420),
    ('crowd.aggregate',    3600,  true,  120),
    ('scoring.recompute',  3600,  true,  240),
    ('alerts.evaluate',     900,  true,   60),
    ('retention.purge',   86400,  true,  600)
ON CONFLICT (job) DO NOTHING;
