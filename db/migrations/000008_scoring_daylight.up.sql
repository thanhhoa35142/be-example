-- Scoring config v2: daylight caps. Spot-hours whose mid-hour sun elevation is
-- below -6 deg are capped at 10 ("dark"), between -6 and 0 deg at 40
-- ("twilight"). Existing v1 rows are rewritten on the next scoring run
-- (config_version and computed_at change on every upsert).
-- t_crowd_curves is read by max(config_version) of its own table, so v1 curves
-- keep applying.
INSERT INTO t_scoring_configs (version, weights, thresholds, active_from)
SELECT 2, weights,
       thresholds || '{"dark_elevation_deg":-6,"dark_score_cap":10,"twilight_elevation_deg":0,"twilight_score_cap":40}'::jsonb,
       now()
FROM t_scoring_configs WHERE version = 1
ON CONFLICT (version) DO NOTHING;
