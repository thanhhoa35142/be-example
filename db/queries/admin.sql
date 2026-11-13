-- name: StartJobRun :one
INSERT INTO t_job_runs (job, started_at, status)
VALUES ($1, now(), 'running')
RETURNING id;

-- name: FinishJobRun :exec
UPDATE t_job_runs SET finished_at = now(), status = $2,
       rows_affected = $3, error = $4, stats = $5
WHERE id = $1;

-- name: ListJobRuns :many
SELECT id, job, started_at, finished_at, status, rows_affected, error
FROM t_job_runs
WHERE (sqlc.narg('before_id')::bigint IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT $1;

-- name: AbandonJobRuns :exec
-- Called after a job wins its advisory lock: any 'running' rows for that job
-- are orphans from a dead worker and move to 'abandoned'.
UPDATE t_job_runs
SET status = 'abandoned', finished_at = now()
WHERE job = $1 AND status = 'running';

-- name: ListJobSchedules :many
SELECT *
FROM t_worker_job_schedules
ORDER BY job;

-- name: GetJobSchedule :one
SELECT *
FROM t_worker_job_schedules
WHERE job = $1;

-- name: UpdateJobSchedule :one
UPDATE t_worker_job_schedules
SET interval_seconds = $2, updated_at = now()
WHERE job = $1
RETURNING *;

-- name: CountActiveSpots :one
SELECT count(*) FROM t_spots WHERE status = 'active';

-- name: CountSpotsWithScore :one
SELECT count(DISTINCT spot_id) FROM t_spot_scores
WHERE ts_hour >= now() - interval '26 hours';

-- name: CountDevices :one
SELECT count(*) FROM t_devices WHERE deleted_at IS NULL;

-- name: CountDevicesActive24h :one
SELECT count(*) FROM t_devices
WHERE deleted_at IS NULL AND last_seen_at > now() - interval '24 hours';

-- name: CountPendingReports :one
SELECT count(*) FROM t_spot_reports WHERE status = 'pending';

-- name: CountReportsToday :one
SELECT count(*) FROM t_spot_reports WHERE created_at > now() - interval '24 hours';

-- name: CountCrowdReportsToday :one
SELECT count(*) FROM t_crowd_reports WHERE created_at > now() - interval '24 hours';

-- name: CountEnabledAlertRulesAdmin :one
SELECT count(*) FROM t_alert_rules WHERE enabled;

-- name: CountAlertDeliveriesToday :one
SELECT count(*) FROM t_alert_deliveries WHERE sent_at > now() - interval '24 hours';

-- name: CountPlusDevices :one
SELECT count(DISTINCT ds.device_id)
FROM t_device_subscriptions ds
JOIN t_subscriptions s ON s.original_transaction_id = ds.original_transaction_id
WHERE s.status IN ('active', 'grace', 'billing_retry');

-- name: MaxWeatherFetchedAt :one
SELECT COALESCE(max(fetched_at), '1970-01-01'::timestamptz) FROM t_weather_hourly_forecasts;

-- name: MaxScoreComputedAt :one
-- Unchanged score rows are not rewritten, so also count the last successful
-- scoring.recompute run as proof the pipeline is alive.
SELECT GREATEST(
    COALESCE((SELECT max(computed_at) FROM t_spot_scores), '1970-01-01'::timestamptz),
    COALESCE((SELECT max(finished_at) FROM t_job_runs
              WHERE job = 'scoring.recompute' AND status = 'succeeded'), '1970-01-01'::timestamptz)
)::timestamptz AS ts;

-- name: MaxCrowdUpdatedAt :one
-- Unchanged model rows are not rewritten, so also count the last successful
-- crowd.refresh run.
SELECT GREATEST(
    COALESCE((SELECT max(updated_at) FROM t_crowd_estimates), '1970-01-01'::timestamptz),
    COALESCE((SELECT max(finished_at) FROM t_job_runs
              WHERE job = 'crowd.refresh' AND status = 'succeeded'), '1970-01-01'::timestamptz)
)::timestamptz AS ts;

-- name: ListPendingSpotReports :many
SELECT r.id, r.spot_id, s.name, r.type, r.issue_code, r.created_at
FROM t_spot_reports r
JOIN t_spots s ON s.id = r.spot_id
WHERE r.status = 'pending'
  AND (sqlc.narg('after_ts')::timestamptz IS NULL
       OR (r.created_at, r.id) > (sqlc.narg('after_ts')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY r.created_at, r.id
LIMIT $1;

-- name: UpdateSpotReportStatus :execrows
UPDATE t_spot_reports SET status = $2 WHERE id = $1;

-- name: GetSpotReportTarget :one
SELECT spot_id, type FROM t_spot_reports WHERE id = $1;

-- name: CloseSpotForReport :execrows
-- Report-driven close: only active/pending spots move; the ingest planner
-- treats status_reason 'closed_by_report' as admin-only (no auto-reopen).
UPDATE t_spots SET status = 'closed', status_reason = 'closed_by_report',
                  updated_at = now()
WHERE id = $1 AND status IN ('active','pending');

-- name: ListScoringConfigs :many
SELECT version, weights, thresholds, active_from
FROM t_scoring_configs
ORDER BY version DESC;

-- name: NextScoringConfigVersion :one
SELECT COALESCE(max(version), 0) + 1 FROM t_scoring_configs;

-- name: InsertScoringConfig :exec
INSERT INTO t_scoring_configs (version, weights, thresholds, active_from)
VALUES ($1, $2, $3, $4);

-- name: ActivateScoringConfig :execrows
UPDATE t_scoring_configs SET active_from = now() WHERE version = $1;

-- name: ScoreDistribution :one
SELECT count(*) AS total,
       COALESCE(avg(ss.score), 0)::float8 AS avg_score,
       count(*) FILTER (WHERE ss.cap_reason IS NOT NULL) AS capped,
       count(DISTINCT ss.spot_id) AS spots
FROM t_spot_scores ss
JOIN t_spots s ON s.id = ss.spot_id
WHERE ss.ts_hour >= sqlc.arg('from_ts') AND ss.ts_hour < sqlc.arg('to_ts')
  AND (sqlc.narg('city_pack_id')::uuid IS NULL OR s.city_pack_id = sqlc.narg('city_pack_id'))
  AND (sqlc.narg('kind')::text IS NULL OR s.kind = sqlc.narg('kind'));

-- name: ScoreHistogram :many
SELECT width_bucket(ss.score, 0, 100, 10) AS bucket, count(*) AS n
FROM t_spot_scores ss
JOIN t_spots s ON s.id = ss.spot_id
WHERE ss.ts_hour >= sqlc.arg('from_ts') AND ss.ts_hour < sqlc.arg('to_ts')
  AND (sqlc.narg('city_pack_id')::uuid IS NULL OR s.city_pack_id = sqlc.narg('city_pack_id'))
  AND (sqlc.narg('kind')::text IS NULL OR s.kind = sqlc.narg('kind'))
GROUP BY bucket ORDER BY bucket;

-- name: FeedbackVerdictCounts :many
SELECT verdict, count(*) AS n
FROM t_score_feedbacks
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('spot_id')::uuid IS NULL OR spot_id = sqlc.narg('spot_id'))
GROUP BY verdict ORDER BY verdict;

-- name: ReportTypeCounts :many
SELECT type, status, count(*) AS n
FROM t_spot_reports
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
GROUP BY type, status ORDER BY type, status;

-- name: CrowdSourceCounts :one
SELECT count(*) FILTER (WHERE source = 'model') AS model,
       count(*) FILTER (WHERE source = 'community') AS community,
       count(*) FILTER (WHERE source = 'provider') AS provider
FROM t_crowd_estimates;

-- name: SubscriptionStatusCounts :many
SELECT status, count(*) AS n
FROM t_subscriptions
GROUP BY status ORDER BY status;

-- name: AlertDeliveryStatusCounts :many
SELECT COALESCE(apns_status, -1) AS apns_status, count(*) AS n
FROM t_alert_deliveries
WHERE sent_at >= sqlc.arg('from_ts') AND sent_at < sqlc.arg('to_ts')
GROUP BY apns_status ORDER BY apns_status;

-- name: AdminEventNameCounts :many
SELECT name, count(*) AS n
FROM t_events
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name'))
GROUP BY name
ORDER BY n DESC
LIMIT 100;

-- name: AdminEventDayCounts :many
SELECT date_trunc('day', created_at)::timestamptz AS day, count(*) AS n
FROM t_events
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
GROUP BY day
ORDER BY day DESC;
