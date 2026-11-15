UPDATE t_plan_limits SET limit_value = -1
WHERE plan = 'plus' AND feature = 'alert_rules';
