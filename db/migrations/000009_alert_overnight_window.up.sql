-- Overnight alert windows (startLocal > endLocal) are supported by the
-- evaluator and documented in the API, but the baseline CHECK rejected them.
ALTER TABLE t_alert_rules DROP CONSTRAINT ck_t_alert_rules_window;
ALTER TABLE t_alert_rules ADD CONSTRAINT ck_t_alert_rules_window CHECK (start_local <> end_local);
