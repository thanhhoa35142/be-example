DELETE FROM t_alert_rules WHERE start_local >= end_local;
ALTER TABLE t_alert_rules DROP CONSTRAINT ck_t_alert_rules_window;
ALTER TABLE t_alert_rules ADD CONSTRAINT ck_t_alert_rules_window CHECK (start_local < end_local);
