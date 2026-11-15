-- name: LockDeviceForPlans :one
-- Row lock that serialises plan creation for one device so the plan cap
-- (count then insert) cannot be raced.
SELECT id FROM t_devices
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: CountActiveOwnedPlans :one
-- Active = not canceled and not yet ended.
SELECT count(*) FROM t_plans
WHERE device_id = $1 AND canceled_at IS NULL AND end_ts > now();

-- name: PlanSpot :one
SELECT id, name, kind, tz FROM t_spots
WHERE id = $1 AND status = 'active';

-- name: InsertPlan :one
INSERT INTO t_plans (share_token, device_id, spot_id, start_ts, end_ts, tz,
                     baseline_score, state, score, cap_reason, notified_state, evaluated_at)
VALUES ($1, $2, $3, $4, $5, $6,
        sqlc.narg(baseline_score), sqlc.narg(state), sqlc.narg(score), sqlc.narg(cap_reason),
        sqlc.narg(state), CASE WHEN sqlc.narg(state)::text IS NULL THEN NULL ELSE now() END)
RETURNING id, created_at;

-- name: ListDevicePlans :many
-- Plans the device owns or follows that ended less than a day ago (or are
-- still to come), canceled ones included so a follower sees the cancel.
SELECT p.id, p.share_token, p.spot_id, s.name AS spot_name, s.kind AS spot_kind,
       p.start_ts, p.end_ts, p.tz, p.baseline_score, p.state, p.score, p.cap_reason,
       p.alternative, p.canceled_at, p.created_at,
       'owner'::text AS role,
       (SELECT count(*) FROM t_plan_followers f WHERE f.plan_id = p.id)::int AS follower_count
FROM t_plans p
JOIN t_spots s ON s.id = p.spot_id
WHERE p.device_id = $1 AND p.end_ts > now() - interval '1 day'
UNION ALL
SELECT p.id, p.share_token, p.spot_id, s.name AS spot_name, s.kind AS spot_kind,
       p.start_ts, p.end_ts, p.tz, p.baseline_score, p.state, p.score, p.cap_reason,
       p.alternative, p.canceled_at, p.created_at,
       'follower'::text AS role,
       0::int AS follower_count
FROM t_plan_followers f
JOIN t_plans p ON p.id = f.plan_id
JOIN t_spots s ON s.id = p.spot_id
WHERE f.device_id = $1 AND p.end_ts > now() - interval '1 day'
ORDER BY start_ts, id;

-- name: CancelPlan :execrows
-- Idempotent: cancelling twice keeps the first timestamp.
UPDATE t_plans SET canceled_at = COALESCE(canceled_at, now())
WHERE id = $1 AND device_id = $2;

-- name: ActivePlanByToken :one
-- Token read: only plans that are neither canceled nor ended. Unknown,
-- canceled and ended tokens all produce no row. Never selects the owner
-- device or followers.
SELECT p.id, p.spot_id, s.name AS spot_name, s.kind AS spot_kind, s.city AS spot_city,
       ST_Y(s.geom::geometry)::float8 AS lat, ST_X(s.geom::geometry)::float8 AS lon,
       p.start_ts, p.end_ts, p.tz, p.baseline_score
FROM t_plans p
JOIN t_spots s ON s.id = p.spot_id
WHERE p.share_token = $1 AND p.canceled_at IS NULL AND p.end_ts > now();

-- name: LockActivePlanByToken :one
-- Follow path: lock the plan row so the follower cap cannot be raced.
SELECT id, device_id FROM t_plans
WHERE share_token = $1 AND canceled_at IS NULL AND end_ts > now()
FOR UPDATE;

-- name: PlanFollowerExists :one
SELECT EXISTS(SELECT 1 FROM t_plan_followers WHERE plan_id = $1 AND device_id = $2);

-- name: CountPlanFollowers :one
SELECT count(*) FROM t_plan_followers WHERE plan_id = $1;

-- name: CountActiveFollows :one
-- Plans the device follows that are not canceled and not ended.
SELECT count(*) FROM t_plan_followers f
JOIN t_plans p ON p.id = f.plan_id
WHERE f.device_id = $1 AND p.canceled_at IS NULL AND p.end_ts > now();

-- name: InsertPlanFollower :exec
INSERT INTO t_plan_followers (plan_id, device_id) VALUES ($1, $2)
ON CONFLICT (plan_id, device_id) DO NOTHING;

-- name: DeletePlanFollowByToken :exec
-- Any plan state: a follower can always leave, even a canceled/ended plan.
DELETE FROM t_plan_followers f
USING t_plans p
WHERE f.plan_id = p.id AND p.share_token = $1 AND f.device_id = $2;

-- name: PlanWindowScores :many
-- Hourly scores covering [from, to): from is the start hour, to the end.
SELECT ts_hour, score, cap_reason, reasons
FROM t_spot_scores
WHERE spot_id = $1 AND ts_hour >= sqlc.arg(from_hour) AND ts_hour < sqlc.arg(to_ts)
ORDER BY ts_hour;

-- name: EvaluablePlans :many
-- plans.evaluate: every plan that is not canceled and has not started, within
-- the forecast range (scores exist at most 7 days ahead).
SELECT p.id, p.share_token, p.spot_id, s.name AS spot_name, s.kind AS spot_kind,
       p.start_ts, p.end_ts, p.tz, p.baseline_score, p.state, p.notified_state,
       p.last_notified_on, p.confirmed_at
FROM t_plans p
JOIN t_spots s ON s.id = p.spot_id
WHERE p.canceled_at IS NULL AND p.start_ts > now()
  AND p.start_ts < sqlc.arg(until)::timestamptz
ORDER BY p.start_ts, p.id;

-- name: SavePlanEvaluation :exec
-- Stores the latest evaluation. The first evaluation with scores (plan
-- created before scores existed) also sets the baseline and the notified
-- state, silently: it is the starting point, not a change.
UPDATE t_plans SET
    state          = sqlc.arg(state),
    score          = sqlc.arg(score),
    cap_reason     = sqlc.narg(cap_reason),
    alternative    = sqlc.narg(alternative),
    evaluated_at   = now(),
    baseline_score = COALESCE(baseline_score, sqlc.arg(score)),
    notified_state = COALESCE(notified_state, sqlc.arg(state))
WHERE id = $1 AND canceled_at IS NULL;

-- name: ClaimPlanChange :one
-- Guarded transition for plan.changed: only one worker, only while the
-- stored state still differs from the last notified one, at most once per
-- local day, never for a canceled or started plan.
UPDATE t_plans SET
    notified_state   = sqlc.arg(state),
    last_notified_on = sqlc.arg(local_date),
    last_notified_at = now()
WHERE id = $1 AND state = sqlc.arg(state)
  AND notified_state IS NOT NULL AND notified_state <> sqlc.arg(state)
  AND last_notified_on IS DISTINCT FROM sqlc.arg(local_date)
  AND canceled_at IS NULL AND start_ts > now()
RETURNING id;

-- name: ClaimPlanConfirm :one
-- Guarded transition for plan.confirmed: once per plan, only while ok and
-- nothing else was pushed for it that local day.
UPDATE t_plans SET
    confirmed_at     = now(),
    last_notified_on = sqlc.arg(local_date),
    last_notified_at = now()
WHERE id = $1 AND state = 'ok' AND notified_state = 'ok' AND confirmed_at IS NULL
  AND last_notified_on IS DISTINCT FROM sqlc.arg(local_date)
  AND canceled_at IS NULL AND start_ts > now()
RETURNING id;

-- name: EnqueuePlanDeliveries :execrows
-- One outbox row for the owner and each follower whose push is active.
INSERT INTO t_plan_deliveries (plan_id, device_id, local_date, kind, payload)
SELECT p.id, d.id, sqlc.arg(local_date)::date, sqlc.arg(kind)::text, sqlc.arg(payload)::jsonb
FROM t_plans p
JOIN LATERAL (
    SELECT p.device_id AS device_id
    UNION
    SELECT f.device_id FROM t_plan_followers f WHERE f.plan_id = p.id
) r ON true
JOIN t_devices d ON d.id = r.device_id AND d.deleted_at IS NULL AND d.push_status = 'active'
WHERE p.id = $1
ON CONFLICT (plan_id, device_id, local_date) DO NOTHING;

-- name: SweepStalePlanDeliveries :exec
-- Claims older than 5 minutes are presumed crashed mid-send.
UPDATE t_plan_deliveries
SET status = 'failed', last_error = 'claim timeout'
WHERE status = 'claimed' AND claimed_at < now() - interval '5 minutes';

-- name: ExpirePlanDeliveries :exec
-- Open rows that may no longer be sent become dead: the plan was canceled
-- or has started, the local day they were meant for is over, or attempts
-- are exhausted.
UPDATE t_plan_deliveries d SET
    status = 'dead',
    last_error = CASE WHEN d.attempts >= sqlc.arg(max_attempts)::int
                      THEN COALESCE(d.last_error, 'attempts exhausted') ELSE 'stale' END
FROM t_plans p
WHERE d.plan_id = p.id AND d.status IN ('pending', 'failed')
  AND (p.canceled_at IS NOT NULL OR p.start_ts <= now()
       OR d.local_date < (now() AT TIME ZONE p.tz)::date
       OR d.attempts >= sqlc.arg(max_attempts)::int);

-- name: ClaimPlanDeliveries :many
WITH c AS (
    SELECT plan_id, device_id, local_date FROM t_plan_deliveries
    WHERE status IN ('pending', 'failed') AND attempts < sqlc.arg(max_attempts)::int
    ORDER BY created_at
    LIMIT sqlc.arg(batch)::int
    FOR UPDATE SKIP LOCKED
)
UPDATE t_plan_deliveries d
SET status = 'claimed', attempts = d.attempts + 1, claimed_at = now()
FROM c
WHERE d.plan_id = c.plan_id AND d.device_id = c.device_id AND d.local_date = c.local_date
RETURNING d.plan_id, d.device_id, d.local_date, d.kind, d.payload;

-- name: MarkPlanDeliverySent :exec
UPDATE t_plan_deliveries
SET status = 'sent', sent_at = now(), apns_status = 200, last_error = NULL
WHERE plan_id = $1 AND device_id = $2 AND local_date = $3 AND status = 'claimed';

-- name: MarkPlanDeliveryFailed :exec
UPDATE t_plan_deliveries
SET status = sqlc.arg(status), apns_status = sqlc.narg(apns_status), last_error = sqlc.arg(last_error)
WHERE plan_id = $1 AND device_id = $2 AND local_date = $3 AND status = 'claimed';

-- name: NearbyPlanAlternative :one
-- Best other active spot within radius_m for the same window: every hour
-- scored, no bad cap, mean at least min_mean; nearest first on ties.
SELECT s.id, s.name, s.kind,
       round(avg(sc.score))::int AS mean
FROM t_spots c
JOIN t_spots s ON s.status = 'active' AND s.id <> c.id
             AND ST_DWithin(s.geom, c.geom, sqlc.arg(radius_m)::float8)
JOIN t_spot_scores sc ON sc.spot_id = s.id
             AND sc.ts_hour >= sqlc.arg(from_hour) AND sc.ts_hour < sqlc.arg(to_ts)
WHERE c.id = $1
GROUP BY s.id, s.name, s.kind
HAVING count(*) = sqlc.arg(hours)::int
   AND NOT COALESCE(bool_or(sc.cap_reason = ANY(sqlc.arg(bad_caps)::text[])), false)
   AND round(avg(sc.score)) >= sqlc.arg(min_mean)::int
ORDER BY mean DESC, min(ST_Distance(s.geom, c.geom)), s.id
LIMIT 1;
