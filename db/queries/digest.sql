-- name: UpsertWeeklyDigest :one
-- Keeps the schedule (send_dow/send_hour) and last_sent_on of an existing row.
INSERT INTO t_weekly_digests (device_id, spot_ids, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (device_id) DO UPDATE SET
    spot_ids   = EXCLUDED.spot_ids,
    enabled    = EXCLUDED.enabled,
    updated_at = now()
RETURNING device_id, spot_ids, enabled, send_dow, send_hour, last_sent_on;

-- name: GetWeeklyDigest :one
SELECT device_id, spot_ids, enabled, send_dow, send_hour, last_sent_on
FROM t_weekly_digests
WHERE device_id = $1;

-- name: DeleteWeeklyDigest :execrows
DELETE FROM t_weekly_digests WHERE device_id = $1;

-- name: ResolveDigestSpotIDs :many
-- Requested id -> active target id (merged ids resolve one hop, same as
-- ListSpotsByIDs). Unknown / non-active ids yield no row.
SELECT m.id AS requested, t.id AS resolved
FROM t_spots m
JOIN t_spots t ON t.id = COALESCE(m.merged_into, m.id)
WHERE m.id = ANY(sqlc.arg(ids)::uuid[])
  AND t.status = 'active';

-- name: ListDigestCandidates :many
-- Enabled digests of live devices that can receive a push. Send-time and
-- plan checks happen in Go (device tz may be NULL/invalid; plan lives in
-- the entitlement module).
SELECT d.device_id, d.spot_ids, d.enabled, d.send_dow, d.send_hour, d.last_sent_on,
       COALESCE(dev.tz, '') AS device_tz
FROM t_weekly_digests d
JOIN t_devices dev ON dev.id = d.device_id
WHERE d.enabled
  AND dev.deleted_at IS NULL
  AND dev.push_status = 'active';

-- name: ClaimWeeklyDigest :one
-- Idempotent send slot: only one runner wins a given local date.
UPDATE t_weekly_digests
SET last_sent_on = sqlc.arg(local_date)::date
WHERE device_id = sqlc.arg(device_id)
  AND enabled
  AND last_sent_on IS NOT DISTINCT FROM sqlc.narg(prev)::date
RETURNING device_id;

-- name: ReleaseWeeklyDigest :exec
-- Undo a claim after a transient send failure so a later run can retry.
UPDATE t_weekly_digests
SET last_sent_on = sqlc.narg(prev)::date
WHERE device_id = sqlc.arg(device_id) AND last_sent_on = sqlc.arg(local_date)::date;

-- name: ListDigestSpots :many
SELECT id, name, tz FROM t_spots
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND status = 'active';

-- name: ListDigestScores :many
SELECT ts_hour, score
FROM t_spot_scores
WHERE spot_id = $1 AND ts_hour >= $2 AND ts_hour < $3
ORDER BY ts_hour;
