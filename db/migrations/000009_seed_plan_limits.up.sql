-- Seed plan limits (spec §15): Free gets 1 alert area, Plus unlimited.
INSERT INTO plan_limit (plan, feature, limit_value) VALUES
    ('free', 'alert_rules', 1),
    ('plus', 'alert_rules', -1)  -- -1 = unlimited
ON CONFLICT (plan, feature) DO NOTHING;
