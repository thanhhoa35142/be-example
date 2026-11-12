-- name: StartJobRun :one
INSERT INTO job_run (job, started_at, status)
VALUES ($1, now(), 'running')
RETURNING id;

-- name: FinishJobRun :exec
UPDATE job_run SET finished_at = now(), status = $2,
       rows_affected = $3, error = $4
WHERE id = $1;

-- name: ListJobRuns :many
SELECT id, job, started_at, finished_at, status, rows_affected, error
FROM job_run
WHERE (sqlc.narg('before_id')::bigint IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT $1;

-- name: ListJobSchedules :many
SELECT job, interval_seconds, updated_at
FROM worker_job_schedule
ORDER BY job;

-- name: GetJobSchedule :one
SELECT job, interval_seconds, updated_at
FROM worker_job_schedule
WHERE job = $1;

-- name: UpdateJobSchedule :one
UPDATE worker_job_schedule
SET interval_seconds = $2, updated_at = now()
WHERE job = $1
RETURNING job, interval_seconds, updated_at;

-- name: CountActiveSpots :one
SELECT count(*) FROM spot WHERE status = 'active';

-- name: CountSpotsWithScore :one
SELECT count(DISTINCT spot_id) FROM spot_score
WHERE ts_hour >= now() - interval '26 hours';

-- name: CountDevices :one
SELECT count(*) FROM device WHERE deleted_at IS NULL;

-- name: CountDevicesActive24h :one
SELECT count(*) FROM device
WHERE deleted_at IS NULL AND last_seen_at > now() - interval '24 hours';

-- name: CountPendingReports :one
SELECT count(*) FROM spot_report WHERE status = 'pending';

-- name: CountReportsToday :one
SELECT count(*) FROM spot_report WHERE created_at > now() - interval '24 hours';

-- name: CountCrowdReportsToday :one
SELECT count(*) FROM crowd_report WHERE created_at > now() - interval '24 hours';

-- name: CountEnabledAlertRulesAdmin :one
SELECT count(*) FROM alert_rule WHERE enabled;

-- name: CountAlertDeliveriesToday :one
SELECT count(*) FROM alert_delivery WHERE sent_at > now() - interval '24 hours';

-- name: CountPlusDevices :one
SELECT count(DISTINCT ds.device_id)
FROM device_subscription ds
JOIN subscription s ON s.original_transaction_id = ds.original_transaction_id
WHERE s.status IN ('active', 'grace', 'billing_retry');

-- name: MaxWeatherFetchedAt :one
SELECT COALESCE(max(fetched_at), '1970-01-01'::timestamptz) FROM weather_hourly;

-- name: MaxScoreComputedAt :one
SELECT COALESCE(max(computed_at), '1970-01-01'::timestamptz) FROM spot_score;

-- name: MaxCrowdUpdatedAt :one
SELECT COALESCE(max(updated_at), '1970-01-01'::timestamptz) FROM crowd_estimate;

-- name: ListPendingSpotReports :many
SELECT r.id, r.spot_id, s.name, r.type, r.issue_code, r.created_at
FROM spot_report r
JOIN spot s ON s.id = r.spot_id
WHERE r.status = 'pending'
  AND (sqlc.narg('after_ts')::timestamptz IS NULL
       OR (r.created_at, r.id) > (sqlc.narg('after_ts')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY r.created_at, r.id
LIMIT $1;

-- name: UpdateSpotReportStatus :execrows
UPDATE spot_report SET status = $2 WHERE id = $1;

-- name: ListScoringConfigs :many
SELECT version, weights, thresholds, active_from
FROM scoring_config
ORDER BY version DESC;

-- name: NextScoringConfigVersion :one
SELECT COALESCE(max(version), 0) + 1 FROM scoring_config;

-- name: InsertScoringConfig :exec
INSERT INTO scoring_config (version, weights, thresholds, active_from)
VALUES ($1, $2, $3, $4);

-- name: ActivateScoringConfig :execrows
UPDATE scoring_config SET active_from = now() WHERE version = $1;

-- name: ScoreDistribution :one
SELECT count(*) AS total,
       COALESCE(avg(ss.score), 0)::float8 AS avg_score,
       count(*) FILTER (WHERE ss.cap_reason IS NOT NULL) AS capped,
       count(DISTINCT ss.spot_id) AS spots
FROM spot_score ss
JOIN spot s ON s.id = ss.spot_id
WHERE ss.ts_hour >= sqlc.arg('from_ts') AND ss.ts_hour < sqlc.arg('to_ts')
  AND (sqlc.narg('city_pack_id')::uuid IS NULL OR s.city_pack_id = sqlc.narg('city_pack_id'))
  AND (sqlc.narg('kind')::text IS NULL OR s.kind = sqlc.narg('kind'));

-- name: ScoreHistogram :many
SELECT width_bucket(ss.score, 0, 100, 10) AS bucket, count(*) AS n
FROM spot_score ss
JOIN spot s ON s.id = ss.spot_id
WHERE ss.ts_hour >= sqlc.arg('from_ts') AND ss.ts_hour < sqlc.arg('to_ts')
  AND (sqlc.narg('city_pack_id')::uuid IS NULL OR s.city_pack_id = sqlc.narg('city_pack_id'))
  AND (sqlc.narg('kind')::text IS NULL OR s.kind = sqlc.narg('kind'))
GROUP BY bucket ORDER BY bucket;

-- name: FeedbackVerdictCounts :many
SELECT verdict, count(*) AS n
FROM score_feedback
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('spot_id')::uuid IS NULL OR spot_id = sqlc.narg('spot_id'))
GROUP BY verdict ORDER BY verdict;

-- name: ReportTypeCounts :many
SELECT type, status, count(*) AS n
FROM spot_report
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
GROUP BY type, status ORDER BY type, status;

-- name: CrowdSourceCounts :one
SELECT count(*) FILTER (WHERE source = 'model') AS model,
       count(*) FILTER (WHERE source = 'community') AS community,
       count(*) FILTER (WHERE source = 'provider') AS provider
FROM crowd_estimate;

-- name: SubscriptionStatusCounts :many
SELECT status, count(*) AS n
FROM subscription
GROUP BY status ORDER BY status;

-- name: AlertDeliveryStatusCounts :many
SELECT COALESCE(apns_status, -1) AS apns_status, count(*) AS n
FROM alert_delivery
WHERE sent_at >= sqlc.arg('from_ts') AND sent_at < sqlc.arg('to_ts')
GROUP BY apns_status ORDER BY apns_status;

-- name: AdminEventNameCounts :many
SELECT name, count(*) AS n
FROM event
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name'))
GROUP BY name
ORDER BY n DESC
LIMIT 100;

-- name: AdminEventDayCounts :many
SELECT date_trunc('day', created_at)::timestamptz AS day, count(*) AS n
FROM event
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
GROUP BY day
ORDER BY day DESC;
