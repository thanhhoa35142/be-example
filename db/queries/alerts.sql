-- name: ListAlertRules :many
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM t_alert_rules
WHERE device_id = $1
ORDER BY id;

-- name: GetAlertRule :one
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM t_alert_rules
WHERE id = $1;

-- name: CountAlertRules :one
SELECT count(*) FROM t_alert_rules WHERE device_id = $1;

-- name: InsertAlertRule :one
INSERT INTO t_alert_rules (device_id, spot_id, h3_cell, radius_m, min_score,
                        days_mask, start_local, end_local, tz)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: UpdateAlertRule :one
UPDATE t_alert_rules SET
    spot_id      = $3,
    h3_cell      = $4,
    radius_m     = $5,
    min_score    = $6,
    days_mask    = $7,
    start_local  = $8,
    end_local    = $9,
    tz           = $10,
    enabled      = $11
WHERE id = $1 AND device_id = $2
RETURNING id;

-- name: DeleteAlertRule :execrows
DELETE FROM t_alert_rules WHERE id = $1 AND device_id = $2;

-- name: ListEnabledAlertRules :many
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM t_alert_rules
WHERE enabled;

-- name: SpotScoreAt :one
SELECT s.id, s.name, ss.score
FROM t_spots s
JOIN t_spot_scores ss ON ss.spot_id = s.id AND ss.ts_hour = $2
WHERE s.id = $1;

-- name: BestScoredSpotNear :one
SELECT s.id, s.name, ss.score
FROM t_spots s
JOIN t_spot_scores ss ON ss.spot_id = s.id AND ss.ts_hour = $3
WHERE ST_DWithin(s.geom::geography,
                 ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $4)
ORDER BY ss.score DESC NULLS LAST
LIMIT 1;

-- name: SweepStaleDeliveries :exec
-- Claims older than 5 minutes are presumed crashed mid-send; mark failed so
-- the retry path can pick them up.
UPDATE t_alert_deliveries
SET status = 'failed', last_error = 'claim timeout'
WHERE status = 'claimed' AND claimed_at < now() - interval '5 minutes';

-- name: DeadLetterFailedDeliveries :exec
-- 'failed' rows that exhausted their reclaim attempts become 'dead'.
UPDATE t_alert_deliveries
SET status = 'dead', last_error = COALESCE(last_error, 'attempts exhausted')
WHERE status = 'failed' AND attempts >= $1;

-- name: ClaimAlertDelivery :one
-- First claim for a (rule, window): inserts a 'claimed' row. Returns no row
-- when the slot already exists in any state.
INSERT INTO t_alert_deliveries (rule_id, window_start, status, claimed_at)
VALUES ($1, $2, 'claimed', now())
ON CONFLICT (rule_id, window_start) DO NOTHING
RETURNING rule_id, window_start;

-- name: ReclaimFailedDelivery :one
-- Retry path: re-claim a 'failed' delivery while attempts remain.
UPDATE t_alert_deliveries
SET status = 'claimed', attempts = attempts + 1, claimed_at = now()
WHERE rule_id = $1 AND window_start = $2
  AND status = 'failed' AND attempts < $3
RETURNING rule_id, window_start;

-- name: MarkDeliverySent :exec
UPDATE t_alert_deliveries
SET status = 'sent', sent_at = now(), apns_status = $3, last_error = NULL
WHERE rule_id = $1 AND window_start = $2 AND status = 'claimed';

-- name: MarkDeliveryFailed :exec
UPDATE t_alert_deliveries
SET status = 'failed', apns_status = $3, last_error = $4
WHERE rule_id = $1 AND window_start = $2 AND status = 'claimed';

-- name: MarkDeliveryDead :exec
UPDATE t_alert_deliveries
SET status = 'dead', last_error = $3
WHERE rule_id = $1 AND window_start = $2 AND status = 'failed';

-- name: DeviceAPNsToken :one
SELECT apns_token, COALESCE(apns_env, 'sandbox'), locale
FROM t_devices
WHERE id = $1 AND deleted_at IS NULL AND push_status = 'active';

-- name: DeviceExistsForAlert :one
SELECT EXISTS(SELECT 1 FROM t_devices WHERE id = $1 AND deleted_at IS NULL);
