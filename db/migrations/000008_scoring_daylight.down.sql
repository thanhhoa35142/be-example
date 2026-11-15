UPDATE t_spot_scores SET config_version = 1 WHERE config_version = 2;
DELETE FROM t_scoring_configs WHERE version = 2;
