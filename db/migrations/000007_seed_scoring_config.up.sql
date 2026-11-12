INSERT INTO scoring_config (version, weights, thresholds, active_from)
VALUES (
    1,
    '{"weather":0.40,"crowd":0.25,"amenity":0.20,"shade":0.10,"fresh":0.05}'::jsonb,
    '{"rain_prob_threshold":60,"rain_score_cap":40,"heat_cap_c":35,"heat_score_cap":30,"cold_cap_c":-10,"cold_score_cap":30}'::jsonb,
    '2026-01-01'::timestamptz
)
ON CONFLICT (version) DO NOTHING;
