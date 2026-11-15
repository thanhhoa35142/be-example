-- Plus alert rules are capped at 20 (was unlimited/-1): an unbounded rule
-- set turns the evaluator into a per-rule query loop — a self-DoS vector.
UPDATE t_plan_limits SET limit_value = 20
WHERE plan = 'plus' AND feature = 'alert_rules';
