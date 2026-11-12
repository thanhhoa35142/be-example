-- Rollback of 000001_baseline: drop triggers/functions, then all t_* tables.

DROP TRIGGER IF EXISTS trg_t_apple_notifications_status ON t_apple_notifications;
DROP TRIGGER IF EXISTS trg_t_job_runs_status ON t_job_runs;
DROP TRIGGER IF EXISTS trg_t_alert_deliveries_status ON t_alert_deliveries;
DROP TRIGGER IF EXISTS trg_t_city_packs_status ON t_city_packs;
DROP TRIGGER IF EXISTS trg_t_spot_reports_status ON t_spot_reports;
DROP TRIGGER IF EXISTS trg_t_spots_status ON t_spots;

DROP FUNCTION IF EXISTS fn_t_apple_notifications_transition();
DROP FUNCTION IF EXISTS fn_t_job_runs_transition();
DROP FUNCTION IF EXISTS fn_t_alert_deliveries_transition();
DROP FUNCTION IF EXISTS fn_t_city_packs_transition();
DROP FUNCTION IF EXISTS fn_t_spot_reports_transition();
DROP FUNCTION IF EXISTS fn_t_spots_transition();
DROP FUNCTION IF EXISTS fn_log_transition(text, text, text, text);
DROP FUNCTION IF EXISTS fn_assert_transition(text[], text, text);

DROP TABLE IF EXISTS t_state_transitions;
DROP TABLE IF EXISTS t_holidays;
DROP TABLE IF EXISTS t_plan_limits;
DROP TABLE IF EXISTS t_provider_quotas;
DROP TABLE IF EXISTS t_worker_job_schedules;
DROP TABLE IF EXISTS t_job_runs;
DROP TABLE IF EXISTS t_events;
DROP TABLE IF EXISTS t_score_feedbacks;
DROP TABLE IF EXISTS t_spot_reports;
DROP TABLE IF EXISTS t_crowd_reports;
DROP TABLE IF EXISTS t_crowd_estimates;
DROP TABLE IF EXISTS t_crowd_curves;
DROP TABLE IF EXISTS t_weather_hourly_forecasts;
DROP TABLE IF EXISTS t_spot_scores;
DROP TABLE IF EXISTS t_scoring_configs;
DROP TABLE IF EXISTS t_api_key_grants;
DROP TABLE IF EXISTS t_api_keys;
DROP TABLE IF EXISTS t_alert_deliveries;
DROP TABLE IF EXISTS t_alert_rules;
DROP TABLE IF EXISTS t_apple_notifications;
DROP TABLE IF EXISTS t_device_subscriptions;
DROP TABLE IF EXISTS t_subscriptions;
DROP TABLE IF EXISTS t_devices;
DROP TABLE IF EXISTS t_spot_sources;
DROP TABLE IF EXISTS t_spot_amenities;
DROP TABLE IF EXISTS t_spots;
DROP TABLE IF EXISTS t_city_packs;
