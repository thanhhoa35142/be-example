-- Good-day push (docs/GOOD_DAY_PUSH_SPEC.md). Every time comparison takes
-- `now` as a parameter so the worker and the tests share one clock.

-- name: UpsertNudgeSubscription :one
-- Full-row upsert: the service merges optional fields with the stored row
-- first. A NULL home_cell (opt-in before a location fix, or an update without
-- home) keeps the stored cell, source and updated-at; the coordinate itself
-- never reaches the database.
INSERT INTO t_nudge_subscriptions (device_id, enabled, home_cell, home_source, home_updated_at, send_hour, consent)
VALUES (sqlc.arg(device_id), sqlc.arg(enabled), sqlc.narg(home_cell)::bigint, sqlc.narg(home_source)::text,
        CASE WHEN sqlc.narg(home_cell)::bigint IS NOT NULL THEN now() END,
        sqlc.arg(send_hour), sqlc.arg(consent))
ON CONFLICT (device_id) DO UPDATE SET
    enabled         = EXCLUDED.enabled,
    home_cell       = COALESCE(EXCLUDED.home_cell, t_nudge_subscriptions.home_cell),
    home_source     = COALESCE(EXCLUDED.home_source, t_nudge_subscriptions.home_source),
    home_updated_at = COALESCE(EXCLUDED.home_updated_at, t_nudge_subscriptions.home_updated_at),
    send_hour       = EXCLUDED.send_hour,
    consent         = EXCLUDED.consent
RETURNING device_id, enabled, home_cell, home_source, home_updated_at, send_hour, consent;

-- name: GetNudgeSubscription :one
SELECT device_id, enabled, home_cell, home_source, home_updated_at, send_hour, consent
FROM t_nudge_subscriptions
WHERE device_id = $1;

-- name: DeleteNudgeSubscription :execrows
DELETE FROM t_nudge_subscriptions WHERE device_id = $1;

-- name: DeleteNudgeDeliveries :exec
-- Opt-out removes the location-derived data together with the home cell: each
-- delivery row names a spot near home. Deliveries hang off t_devices, not off
-- the subscription, so there is no cascade. The push ledger stays: it holds no
-- location and the 20 h cap must still hold after a quick opt-out/opt-in.
DELETE FROM t_nudge_deliveries WHERE device_id = $1;

-- name: ListNudgeCandidates :many
-- Devices that pass every rule that does not depend on the device's local
-- clock (spec §4 rules 1-5). The local send hour / weekday / date, the Friday
-- slot and the "this week" skips are decided in Go with time.LoadLocation: a
-- junk client-supplied tz must not make an AT TIME ZONE cast fail the whole
-- query (same reason ListDigestCandidates leaves send-time to Go).
--
-- ignored_streak is the *effective* streak: 0 when the app was opened after
-- the last nudge, else the stored streak + 1 (the last nudge was ignored and
-- is only folded into the stored value when the next send is marked).
SELECT c.device_id,
       c.device_tz,
       c.home_cell,
       c.send_hour,
       c.last_sent_at,
       c.ignored_streak,
       EXISTS (SELECT 1 FROM t_weekly_digests w
               WHERE w.device_id = c.device_id AND w.enabled) AS digest_enabled,
       -- 'epoch' (zero time) when the device had no digest in the last 8 days.
       COALESCE((SELECT max(p.sent_at) FROM t_push_sends p
                 WHERE p.device_id = c.device_id AND p.kind = 'digest.weekend'
                   AND p.sent_at > sqlc.arg(now)::timestamptz - interval '8 days'),
                'epoch'::timestamptz)::timestamptz AS digest_sent_at,
       (SELECT max(x.local_date) FROM t_nudge_deliveries x
        WHERE x.device_id = c.device_id AND x.slot = 'weekend' AND x.status = 'sent')::date AS weekend_nudge_date,
       (SELECT l.local_date FROM t_nudge_deliveries l
        WHERE l.device_id = c.device_id ORDER BY l.local_date DESC LIMIT 1)::date AS last_delivery_date,
       COALESCE((SELECT l.status FROM t_nudge_deliveries l
                 WHERE l.device_id = c.device_id ORDER BY l.local_date DESC LIMIT 1), '')::text AS last_delivery_status,
       COALESCE((SELECT l.attempts FROM t_nudge_deliveries l
                 WHERE l.device_id = c.device_id ORDER BY l.local_date DESC LIMIT 1), 0)::int AS last_delivery_attempts
FROM (
    SELECT sub.device_id,
           COALESCE(dev.tz, '')::text AS device_tz,
           sub.home_cell,
           sub.send_hour,
           sub.last_sent_at,
           (CASE WHEN sub.last_sent_at IS NULL OR dev.last_seen_at > sub.last_sent_at THEN 0
                 ELSE sub.ignored_streak + 1 END)::int AS ignored_streak
    FROM t_nudge_subscriptions sub
    JOIN t_devices dev ON dev.id = sub.device_id
    WHERE sub.enabled
      AND sub.home_cell IS NOT NULL
      AND dev.deleted_at IS NULL
      AND dev.push_status = 'active'
      AND (dev.last_seen_at IS NULL OR dev.last_seen_at < sqlc.arg(now)::timestamptz - interval '24 hours')
) c
WHERE NOT EXISTS (SELECT 1 FROM t_push_sends p
                  WHERE p.device_id = c.device_id
                    AND p.sent_at > sqlc.arg(now)::timestamptz - interval '20 hours')
  AND (SELECT count(*) FROM t_push_sends p
       WHERE p.device_id = c.device_id AND p.kind = 'nudge.goodday'
         AND p.sent_at > sqlc.arg(now)::timestamptz - interval '7 days') < 2
  AND c.ignored_streak < 6
  AND (c.ignored_streak < 3
       OR c.last_sent_at IS NULL
       OR c.last_sent_at < sqlc.arg(now)::timestamptz - interval '14 days')
ORDER BY c.home_cell, c.device_id;

-- name: ListNudgeScoresNear :many
-- Hourly scores of the best named active spots around a point, in one query
-- per cell. Spots are ranked by ingest quality (no quality last), then
-- distance, and capped at 50. The Go side buckets hours into spot-local days.
WITH p AS (
    SELECT ST_SetSRID(ST_MakePoint(sqlc.arg(lon)::float8, sqlc.arg(lat)::float8), 4326)::geography AS pt
), near AS (
    SELECT s.id, s.name, s.tz, s.quality, ST_Distance(s.geom, p.pt) AS dist
    FROM t_spots s, p
    WHERE s.status = 'active'
      AND s.name IS NOT NULL AND s.name <> ''
      AND ST_DWithin(s.geom, p.pt, sqlc.arg(radius_m)::float8)
    ORDER BY s.quality DESC NULLS LAST, dist, s.id
    LIMIT 50
)
SELECT n.id AS spot_id,
       n.name::text AS name,
       n.tz,
       COALESCE(n.quality, 0)::int AS quality,
       n.dist::float8 AS distance_m,
       ss.ts_hour,
       ss.score
FROM near n
JOIN t_spot_scores ss ON ss.spot_id = n.id
 AND ss.ts_hour >= sqlc.arg(from_ts)::timestamptz
 AND ss.ts_hour <  sqlc.arg(to_ts)::timestamptz
ORDER BY n.id, ss.ts_hour;

-- name: SweepStaleNudgeClaims :exec
-- Claims older than 5 minutes are presumed crashed mid-send.
UPDATE t_nudge_deliveries
SET status = 'failed', last_error = 'claim timeout'
WHERE status = 'claimed' AND claimed_at < sqlc.arg(now)::timestamptz - interval '5 minutes';

-- name: DeadLetterNudgeDeliveries :exec
-- 'failed' rows that exhausted their attempts become 'dead'.
UPDATE t_nudge_deliveries
SET status = 'dead', last_error = COALESCE(last_error, 'attempts exhausted')
WHERE status = 'failed' AND attempts >= $1;

-- name: ClaimNudgeDelivery :one
-- First claim for (device, local day); it is attempt 1. Returns no row when the day already
-- has a delivery in any state.
INSERT INTO t_nudge_deliveries (device_id, local_date, slot, target_date, spot_id,
                                window_start, window_end, score, status, attempts, claimed_at)
VALUES (sqlc.arg(device_id), sqlc.arg(local_date), sqlc.arg(slot), sqlc.arg(target_date), sqlc.arg(spot_id),
        sqlc.arg(window_start), sqlc.arg(window_end), sqlc.arg(score), 'claimed', 1, sqlc.arg(now))
ON CONFLICT (device_id, local_date) DO NOTHING
RETURNING device_id;

-- name: ReclaimFailedNudgeDelivery :one
-- Retry path: re-claim a 'failed' delivery (with the freshly picked spot)
-- while attempts remain.
UPDATE t_nudge_deliveries
SET status = 'claimed', attempts = attempts + 1, claimed_at = sqlc.arg(now),
    slot = sqlc.arg(slot), target_date = sqlc.arg(target_date), spot_id = sqlc.arg(spot_id),
    window_start = sqlc.arg(window_start), window_end = sqlc.arg(window_end), score = sqlc.arg(score)
WHERE device_id = sqlc.arg(device_id) AND local_date = sqlc.arg(local_date)
  AND status = 'failed' AND attempts < sqlc.arg(max_attempts)
RETURNING device_id;

-- name: MarkNudgeDeliverySent :exec
UPDATE t_nudge_deliveries
SET status = 'sent', sent_at = sqlc.arg(sent_at), apns_status = sqlc.arg(apns_status), last_error = NULL
WHERE device_id = sqlc.arg(device_id) AND local_date = sqlc.arg(local_date) AND status = 'claimed';

-- name: MarkNudgeDeliveryFailed :exec
UPDATE t_nudge_deliveries
SET status = 'failed', apns_status = sqlc.arg(apns_status), last_error = sqlc.arg(last_error)
WHERE device_id = sqlc.arg(device_id) AND local_date = sqlc.arg(local_date) AND status = 'claimed';

-- name: MarkNudgeDeliveryDead :exec
UPDATE t_nudge_deliveries
SET status = 'dead', apns_status = sqlc.arg(apns_status), last_error = sqlc.arg(last_error)
WHERE device_id = sqlc.arg(device_id) AND local_date = sqlc.arg(local_date) AND status IN ('claimed', 'failed');

-- name: SetNudgeSent :exec
-- Folds the effective ignored streak into the row together with the send
-- time, once per sent nudge (a failed attempt never touches the streak).
UPDATE t_nudge_subscriptions
SET last_sent_at = sqlc.arg(sent_at), ignored_streak = sqlc.arg(ignored_streak)
WHERE device_id = sqlc.arg(device_id);

-- name: InsertPushSend :exec
INSERT INTO t_push_sends (device_id, kind, sent_at)
VALUES (sqlc.arg(device_id), sqlc.arg(kind), sqlc.arg(sent_at));
