-- name: PurgeWeatherForecasts :execrows
-- Delete up to $2 forecast rows older than $1 (batch; caller loops).
DELETE FROM t_weather_hourly_forecasts
WHERE ctid IN (
    SELECT ctid FROM t_weather_hourly_forecasts AS p
    WHERE p.ts < $1 LIMIT $2
);

-- name: PurgeSpotScores :execrows
DELETE FROM t_spot_scores
WHERE ctid IN (
    SELECT ctid FROM t_spot_scores AS p
    WHERE p.ts_hour < $1 LIMIT $2
);

-- name: PurgeEvents :execrows
DELETE FROM t_events
WHERE ctid IN (
    SELECT ctid FROM t_events AS p
    WHERE p.created_at < $1 LIMIT $2
);

-- name: PurgeCrowdReports :execrows
DELETE FROM t_crowd_reports
WHERE ctid IN (
    SELECT ctid FROM t_crowd_reports AS p
    WHERE p.created_at < $1 LIMIT $2
);

-- name: PurgeJobRuns :execrows
DELETE FROM t_job_runs
WHERE ctid IN (
    SELECT ctid FROM t_job_runs AS p
    WHERE p.started_at < $1 LIMIT $2
);

-- name: PurgeAlertDeliveries :execrows
DELETE FROM t_alert_deliveries
WHERE ctid IN (
    SELECT ctid FROM t_alert_deliveries AS p
    WHERE p.window_start < $1 LIMIT $2
);

-- name: PurgePushSends :execrows
DELETE FROM t_push_sends
WHERE ctid IN (
    SELECT ctid FROM t_push_sends AS p
    WHERE p.sent_at < $1 LIMIT $2
);

-- name: PurgeNudgeDeliveries :execrows
-- Each row names a spot near a device's home, so it must not live forever.
-- Eligibility only looks at the latest delivery and at this ISO week's sent
-- weekend nudge, i.e. at most ~8 days back.
DELETE FROM t_nudge_deliveries
WHERE ctid IN (
    SELECT ctid FROM t_nudge_deliveries AS d
    WHERE d.local_date < sqlc.arg(before)::date LIMIT sqlc.arg(row_limit)
);

-- name: ScrubAppleNotificationPayloads :execrows
-- Keep the audit row but drop the (large) JWS payload after $1.
UPDATE t_apple_notifications
SET payload = '{}'::jsonb
WHERE notification_uuid IN (
    SELECT notification_uuid FROM t_apple_notifications AS n
    WHERE n.received_at < $1 AND n.payload <> '{}'::jsonb
    LIMIT $2
);
