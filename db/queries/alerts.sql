-- name: ListAlertRules :many
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM alert_rule
WHERE device_id = $1
ORDER BY id;

-- name: GetAlertRule :one
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM alert_rule
WHERE id = $1;

-- name: CountAlertRules :one
SELECT count(*) FROM alert_rule WHERE device_id = $1;

-- name: InsertAlertRule :one
INSERT INTO alert_rule (device_id, spot_id, h3_cell, radius_m, min_score,
                        days_mask, start_local, end_local, tz)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: UpdateAlertRule :one
UPDATE alert_rule SET
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
DELETE FROM alert_rule WHERE id = $1 AND device_id = $2;

-- name: ListEnabledAlertRules :many
SELECT id, device_id, spot_id, h3_cell, radius_m, min_score, days_mask,
       start_local, end_local, tz, enabled
FROM alert_rule
WHERE enabled;

-- name: SpotScoreAt :one
SELECT s.id, s.name, ss.score
FROM spot s
JOIN spot_score ss ON ss.spot_id = s.id AND ss.ts_hour = $2
WHERE s.id = $1;

-- name: BestScoredSpotNear :one
SELECT s.id, s.name, ss.score
FROM spot s
JOIN spot_score ss ON ss.spot_id = s.id AND ss.ts_hour = $3
WHERE ST_DWithin(s.geom::geography,
                 ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $4)
ORDER BY ss.score DESC NULLS LAST
LIMIT 1;

-- name: InsertAlertDelivery :execrows
INSERT INTO alert_delivery (rule_id, window_start, sent_at, apns_status)
VALUES ($1, $2, now(), $3)
ON CONFLICT (rule_id, window_start) DO NOTHING;

-- name: SetDeliveryStatus :exec
UPDATE alert_delivery SET apns_status = $3
WHERE rule_id = $1 AND window_start = $2;

-- name: DeviceAPNsToken :one
SELECT apns_token, COALESCE(apns_env, 'sandbox'), locale
FROM device
WHERE id = $1 AND deleted_at IS NULL AND apns_token IS NOT NULL;

-- name: DeviceExistsForAlert :one
SELECT EXISTS(SELECT 1 FROM device WHERE id = $1 AND deleted_at IS NULL);
