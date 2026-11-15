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
