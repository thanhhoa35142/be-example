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
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY id DESC
LIMIT $1;

-- name: InsertAdminAuditEvent :exec
INSERT INTO t_admin_audit_events
    (admin_user_id, auth_method, method, path, status, client_ip, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAdminAuditEvents :many
SELECT e.id, e.admin_user_id, u.email, e.auth_method, e.method, e.path,
       e.status, e.client_ip, e.request_id, e.created_at
FROM t_admin_audit_events e
LEFT JOIN t_admin_users u ON u.id = e.admin_user_id
WHERE (sqlc.narg('before_id')::bigint IS NULL OR e.id < sqlc.narg('before_id'))
ORDER BY e.id DESC
LIMIT $1;

-- name: GetJobRun :one
SELECT id, job, city_pack_id, started_at, finished_at, status,
       rows_affected, error, stats
FROM t_job_runs
WHERE id = $1;

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
-- Only pending reports can transition; the DB trigger enforces the same
-- rule, this WHERE just turns a duplicate moderation into 0 rows instead of
-- a trigger exception.
UPDATE t_spot_reports SET status = $2 WHERE id = $1 AND status = 'pending';

-- name: GetSpotReportTarget :one
SELECT spot_id, type, status FROM t_spot_reports WHERE id = $1;

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
-- Buckets are UTC midnights regardless of the session TimeZone: the inner
-- AT TIME ZONE converts to a naive UTC timestamp, date_trunc floors it, and
-- the outer AT TIME ZONE converts back to timestamptz.
SELECT (date_trunc('day', created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day, count(*) AS n
FROM t_events
WHERE created_at >= sqlc.arg('from_ts') AND created_at < sqlc.arg('to_ts')
GROUP BY day
ORDER BY day DESC;

-- ---- admin console: run-now, plan limits, city packs, spot lookup ----

-- name: RequestJobRun :one
-- A generic run request clears any pending per-pack request so a stale pack
-- slug can never leak into an unrelated run.
UPDATE t_worker_job_schedules
SET run_requested_at = now(), run_requested_pack = NULL
WHERE job = $1
RETURNING *;

-- name: RequestJobRunForPack :one
UPDATE t_worker_job_schedules
SET run_requested_at = now(), run_requested_pack = $2
WHERE job = $1
RETURNING *;

-- name: ClaimJobRunRequest :one
-- Atomic claim: with several worker replicas only one sees a row. Returns the
-- requested pack slug (NULL for a generic run); pgx.ErrNoRows means nothing
-- was requested.
UPDATE t_worker_job_schedules
SET run_requested_at = NULL, run_requested_pack = NULL
WHERE job = $1 AND run_requested_at IS NOT NULL
RETURNING run_requested_pack;

-- name: ListPlanLimits :many
SELECT plan, feature, limit_value
FROM t_plan_limits
ORDER BY feature, plan;

-- name: UpdatePlanLimit :execrows
UPDATE t_plan_limits
SET limit_value = $3
WHERE plan = $1 AND feature = $2;

-- name: ListAdminCityPacks :many
SELECT p.id, p.slug, p.name, p.country, p.tz, p.status, p.last_import_at,
       (SELECT count(*) FROM t_spots s
         WHERE s.city_pack_id = p.id AND s.status = 'active')::bigint AS active_spots,
       (SELECT count(*) FROM t_spots s
         WHERE s.city_pack_id = p.id)::bigint AS total_spots
FROM t_city_packs p
ORDER BY p.country, p.name;

-- name: GetCityPackStatus :one
SELECT status, last_import_at FROM t_city_packs WHERE id = $1;

-- name: UpdateCityPackStatus :execrows
UPDATE t_city_packs SET status = $2 WHERE id = $1;

-- name: GetAdminCityPack :one
SELECT id, slug, name, country, tz, status, osm_extract,
       last_import_at, extract_fetched_at
FROM t_city_packs
WHERE id = $1;

-- name: CountAdminCityPackSpotsByStatus :many
SELECT status, count(*)::bigint AS count
FROM t_spots
WHERE city_pack_id = $1
GROUP BY status;

-- name: CountAdminCityPackIngestFeatures :many
SELECT role, count(*)::bigint AS count
FROM t_ingest_features
WHERE pack_id = $1
GROUP BY role;

-- name: CountAdminCityPackExcludeReasons :many
SELECT exclude_reason, count(*)::bigint AS count
FROM t_ingest_features
WHERE pack_id = $1 AND role = 'excluded' AND exclude_reason IS NOT NULL
GROUP BY exclude_reason
ORDER BY count DESC
LIMIT 10;

-- name: CountAdminCityPackPendingReports :one
SELECT count(*)::bigint
FROM t_spot_reports r
JOIN t_spots s ON s.id = r.spot_id
WHERE s.city_pack_id = $1 AND r.status = 'pending';

-- name: ListAdminCityPackJobRuns :many
SELECT id, job, city_pack_id, started_at, finished_at, status,
       rows_affected, error, stats
FROM t_job_runs
WHERE city_pack_id = $1
ORDER BY id DESC
LIMIT $2;

-- name: ListAdminCityPackSpots :many
SELECT id, name, kind, status, updated_at
FROM t_spots
WHERE city_pack_id = $1
ORDER BY updated_at DESC
LIMIT $2;

-- name: SearchAdminSpots :many
-- Staff lookup across every status. `q` is an already-escaped ILIKE pattern.
SELECT s.id, s.name, s.kind, s.city, s.country, s.status, s.status_reason,
       s.curated, s.last_verified_at, s.updated_at,
       ST_Y(s.geom::geometry)::float8 AS lat,
       ST_X(s.geom::geometry)::float8 AS lon,
       p.slug AS pack_slug, p.name AS pack_name
FROM t_spots s
JOIN t_city_packs p ON p.id = s.city_pack_id
WHERE (sqlc.narg('id')::uuid IS NULL OR s.id = sqlc.narg('id'))
  AND (sqlc.narg('q')::text IS NULL OR s.name ILIKE sqlc.narg('q'))
  AND (sqlc.narg('status')::text IS NULL OR s.status = sqlc.narg('status'))
  AND (sqlc.narg('pack_slug')::text IS NULL OR p.slug = sqlc.narg('pack_slug'))
ORDER BY s.name NULLS LAST, s.id
LIMIT sqlc.arg('row_limit');

-- name: GetSpotStatus :one
SELECT status FROM t_spots WHERE id = $1;

-- name: AdminSetSpotStatus :execrows
-- Admin-owned status change. The reason marks the row as staff-decided so
-- the ingest planner never re-evaluates it.
UPDATE t_spots
SET status = sqlc.arg('status'), status_reason = sqlc.narg('reason'),
    status_changed_at = now(), updated_at = now()
WHERE id = sqlc.arg('id') AND status = ANY(sqlc.arg('from_statuses')::text[]);

-- name: GetAdminSpotByID :one
SELECT s.id, s.city_pack_id, s.kind, s.name, s.names,
       ST_X(s.geom::geometry)::float8 AS lon,
       ST_Y(s.geom::geometry)::float8 AS lat,
       ST_AsGeoJSON(s.area)::json AS area_geojson,
       s.country, s.city, s.tz, s.cell_id, s.status, s.status_reason,
       s.status_changed_at, s.quality, s.attrs, s.curated,
       s.merged_into, s.last_verified_at, s.created_at, s.updated_at,
       p.slug AS pack_slug, p.name AS pack_name,
       m.name AS merged_into_name
FROM t_spots s
JOIN t_city_packs p ON p.id = s.city_pack_id
LEFT JOIN t_spots m ON m.id = s.merged_into
WHERE s.id = $1;

-- name: ListAdminSpotTransitions :many
SELECT from_status, to_status, changed_at
FROM t_state_transitions
WHERE entity = 'spot' AND entity_id = $1
ORDER BY changed_at DESC, id DESC
LIMIT $2;

-- name: ListAdminSpotScores :many
SELECT ts_hour, score, confidence, cap_reason, reasons, computed_at
FROM t_spot_scores
WHERE spot_id = $1
ORDER BY ts_hour DESC
LIMIT $2;

-- name: ListAdminSpotAuditEvents :many
SELECT e.id, e.admin_user_id, u.email, e.auth_method, e.method, e.path,
       e.status, e.client_ip, e.request_id, e.created_at
FROM t_admin_audit_events e
LEFT JOIN t_admin_users u ON u.id = e.admin_user_id
WHERE e.path = $1
ORDER BY e.id DESC
LIMIT $2;

-- name: SetJobScheduleEnabled :one
UPDATE t_worker_job_schedules
SET enabled = $2, updated_at = now()
WHERE job = $1
RETURNING *;

-- ---- admin console: moderation history, ops lists, provider health ----

-- name: ListSpotReportsAdmin :many
-- Full report history (any status), newest first. Keyset on
-- (created_at, id) DESC — same cursor shape as the pending list.
SELECT r.id, r.spot_id, s.name AS spot_name, r.device_id, r.type, r.issue_code,
       r.status, r.reviewed_at, r.created_at
FROM t_spot_reports r
JOIN t_spots s ON s.id = r.spot_id
WHERE (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status'))
  AND (sqlc.narg('type')::text IS NULL OR r.type = sqlc.narg('type'))
  AND (sqlc.narg('spot_id')::uuid IS NULL OR r.spot_id = sqlc.narg('spot_id'))
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR r.created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR r.created_at < sqlc.narg('to_ts'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (r.created_at, r.id) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY r.created_at DESC, r.id DESC
LIMIT $1;

-- name: GetSpotReportAdmin :one
SELECT r.id, r.spot_id, s.name AS spot_name, r.device_id, r.type, r.issue_code,
       r.status, r.reviewed_at, r.created_at
FROM t_spot_reports r
JOIN t_spots s ON s.id = r.spot_id
WHERE r.id = $1;

-- name: ListCrowdReportsAdmin :many
SELECT cr.id, cr.spot_id, s.name AS spot_name, cr.device_id, cr.level,
       cr.local_dow, cr.local_hour, cr.created_at
FROM t_crowd_reports cr
JOIN t_spots s ON s.id = cr.spot_id
WHERE (sqlc.narg('spot_id')::uuid IS NULL OR cr.spot_id = sqlc.narg('spot_id'))
  AND (sqlc.narg('device_id')::uuid IS NULL OR cr.device_id = sqlc.narg('device_id'))
  AND (sqlc.narg('level')::smallint IS NULL OR cr.level = sqlc.narg('level'))
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR cr.created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR cr.created_at < sqlc.narg('to_ts'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (cr.created_at, cr.id) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY cr.created_at DESC, cr.id DESC
LIMIT $1;

-- name: ListScoreFeedbacksAdmin :many
SELECT f.id, f.spot_id, s.name AS spot_name, f.ts_hour, f.device_id, f.verdict,
       f.created_at
FROM t_score_feedbacks f
JOIN t_spots s ON s.id = f.spot_id
WHERE (sqlc.narg('spot_id')::uuid IS NULL OR f.spot_id = sqlc.narg('spot_id'))
  AND (sqlc.narg('device_id')::uuid IS NULL OR f.device_id = sqlc.narg('device_id'))
  AND (sqlc.narg('verdict')::smallint IS NULL OR f.verdict = sqlc.narg('verdict'))
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR f.created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR f.created_at < sqlc.narg('to_ts'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (f.created_at, f.id) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY f.created_at DESC, f.id DESC
LIMIT $1;

-- name: ListAdminDevices :many
SELECT d.id, d.apns_env, d.push_status, d.locale, d.tz, d.app_version,
       d.created_at, d.last_seen_at, d.deleted_at,
       (SELECT count(*) FROM t_device_subscriptions ds
         WHERE ds.device_id = d.id)::bigint AS subscriptions
FROM t_devices d
WHERE (sqlc.narg('id')::uuid IS NULL OR d.id = sqlc.narg('id'))
  AND (sqlc.narg('push_status')::text IS NULL OR d.push_status = sqlc.narg('push_status'))
  AND (sqlc.arg('include_deleted')::bool OR d.deleted_at IS NULL)
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (d.created_at, d.id) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY d.created_at DESC, d.id DESC
LIMIT $1;

-- name: GetAdminDevice :one
SELECT id, apns_env, push_status, locale, tz, app_version, prefs,
       attest_key_id, created_at, last_seen_at, deleted_at
FROM t_devices
WHERE id = $1;

-- name: ListAdminDeviceSubscriptions :many
SELECT ds.original_transaction_id, ds.linked_at,
       s.product_id, s.status, s.environment, s.ownership, s.is_trial,
       s.auto_renew, s.expires_at, s.grace_expires_at, s.revoked_at,
       s.last_signed_date
FROM t_device_subscriptions ds
JOIN t_subscriptions s ON s.original_transaction_id = ds.original_transaction_id
WHERE ds.device_id = $1
ORDER BY ds.linked_at DESC;

-- name: ListAlertDeliveriesAdmin :many
-- Dead-lettered pushes included: status filter covers claimed/sent/failed/dead.
SELECT d.rule_id, d.window_start, d.status, d.attempts, d.claimed_at, d.sent_at,
       d.apns_status, d.last_error, r.device_id, r.spot_id
FROM t_alert_deliveries d
JOIN t_alert_rules r ON r.id = d.rule_id
WHERE (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status'))
  AND (sqlc.narg('rule_id')::uuid IS NULL OR d.rule_id = sqlc.narg('rule_id'))
  AND (sqlc.narg('device_id')::uuid IS NULL OR r.device_id = sqlc.narg('device_id'))
  AND (sqlc.narg('apns_status')::int IS NULL OR d.apns_status = sqlc.narg('apns_status'))
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR d.window_start >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR d.window_start < sqlc.narg('to_ts'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (d.window_start, d.rule_id) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY d.window_start DESC, d.rule_id DESC
LIMIT $1;

-- name: ListAppleNotificationsAdmin :many
SELECT notification_uuid, type, subtype, original_transaction_id, signed_date,
       environment, status, attempts, last_error, received_at, processed_at
FROM t_apple_notifications
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('environment')::text IS NULL OR environment = sqlc.narg('environment'))
  AND (sqlc.narg('txn')::text IS NULL OR original_transaction_id = sqlc.narg('txn'))
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR received_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR received_at < sqlc.narg('to_ts'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (received_at, notification_uuid) < (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_id')::uuid))
ORDER BY received_at DESC, notification_uuid DESC
LIMIT $1;

-- name: ListProviderQuotas :many
SELECT provider, day, used, updated_at
FROM t_provider_quotas
WHERE day >= $1
ORDER BY day DESC, provider;

-- name: ListRecentJobRuns :many
SELECT id, job, city_pack_id, started_at, finished_at, status,
       rows_affected, error, stats
FROM t_job_runs
WHERE job = $1
ORDER BY id DESC
LIMIT $2;

-- name: ListIngestFeaturesAdmin :many
-- Raw OSM feature layer, newest sightings first. The keyset cursor spans
-- (seen_at, source, source_id) so ties inside one run stay stable.
SELECT f.pack_id, p.slug AS pack_slug, f.source, f.source_id, f.role, f.kind,
       f.amenity, f.quality, f.exclude_reason, f.version, f.run_id, f.seen_at
FROM t_ingest_features f
JOIN t_city_packs p ON p.id = f.pack_id
WHERE (sqlc.narg('pack_slug')::text IS NULL OR p.slug = sqlc.narg('pack_slug'))
  AND (sqlc.narg('role')::text IS NULL OR f.role = sqlc.narg('role'))
  AND (sqlc.narg('source')::text IS NULL OR f.source = sqlc.narg('source'))
  AND (sqlc.narg('run_id')::bigint IS NULL OR f.run_id = sqlc.narg('run_id'))
  AND (sqlc.narg('before_ts')::timestamptz IS NULL
       OR (f.seen_at, f.source, f.source_id) <
          (sqlc.narg('before_ts')::timestamptz, sqlc.narg('before_source')::text, sqlc.narg('before_source_id')::text))
ORDER BY f.seen_at DESC, f.source DESC, f.source_id DESC
LIMIT $1;

-- name: GetCityPackBySlug :one
SELECT id, slug, status FROM t_city_packs WHERE slug = $1;

-- ---- admin console: spot lifecycle ----

-- name: AdminSetSpotCurated :execrows
UPDATE t_spots SET curated = $2, updated_at = now()
WHERE id = $1 AND curated IS DISTINCT FROM $2;

-- name: AdminUpdateSpotName :execrows
UPDATE t_spots SET name = $2, updated_at = now()
WHERE id = $1 AND name IS DISTINCT FROM $2;

-- name: GetSpotPackAndStatus :one
SELECT city_pack_id, status, merged_into FROM t_spots WHERE id = $1;

-- name: AdminMergeSpot :execrows
-- Merge is ingest-owned in spirit but the column pair is simple: mark the row
-- merged and point it at the surviving spot. The merged_pair CHECK keeps the
-- two columns consistent in one statement.
UPDATE t_spots SET status = 'merged', merged_into = $2,
    status_reason = 'merged_by_admin', status_changed_at = now(), updated_at = now()
WHERE id = $1 AND status <> 'merged' AND merged_into IS NULL;

-- name: AdminUnmergeSpot :execrows
-- Back to pending (not the unknowable previous status) so the next ingest
-- run re-evaluates the spot from its sources.
UPDATE t_spots SET status = 'pending', merged_into = NULL, status_reason = NULL,
    status_changed_at = now(), updated_at = now()
WHERE id = $1 AND status = 'merged' AND merged_into IS NOT NULL;
