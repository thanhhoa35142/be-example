-- name: UpsertDevice :one
-- push_status tracks token state: providing apns_token marks the device
-- 'active'; omitting it keeps the current status/token pair intact.
INSERT INTO t_devices (id, apns_token, apns_env, locale, tz, app_version, prefs,
                       push_status, push_status_changed_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        CASE WHEN $2::text IS NOT NULL THEN 'active' ELSE 'none' END,
        CASE WHEN $2::text IS NOT NULL THEN now() END,
        now())
ON CONFLICT (id) DO UPDATE SET
    apns_token   = COALESCE(EXCLUDED.apns_token, t_devices.apns_token),
    apns_env     = COALESCE(EXCLUDED.apns_env, t_devices.apns_env),
    locale       = COALESCE(EXCLUDED.locale, t_devices.locale),
    tz           = COALESCE(EXCLUDED.tz, t_devices.tz),
    app_version  = COALESCE(EXCLUDED.app_version, t_devices.app_version),
    prefs        = CASE WHEN EXCLUDED.prefs = '{}'::jsonb THEN t_devices.prefs ELSE EXCLUDED.prefs END,
    push_status  = CASE WHEN EXCLUDED.apns_token IS NOT NULL THEN 'active' ELSE t_devices.push_status END,
    push_status_changed_at = CASE
        WHEN EXCLUDED.apns_token IS NOT NULL
             AND t_devices.apns_token IS DISTINCT FROM EXCLUDED.apns_token
        THEN now() ELSE t_devices.push_status_changed_at END,
    last_seen_at = now(),
    deleted_at   = NULL
RETURNING id;

-- name: GetActiveDevice :one
SELECT id FROM t_devices
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetDeviceTz :one
SELECT COALESCE(tz, 'UTC') FROM t_devices
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteDevice :execrows
-- "Delete my data": the device is soft-deleted (the row is kept so anonymous
-- aggregates referencing it stay valid) while everything personal goes in the
-- same statement: weekly digest, alert rules (t_alert_deliveries cascade from
-- the rules), analytics events (t_events, device_id-keyed), and the push token / attestation key / prefs on the device row.
-- Soft delete never fires the device FK cascades, hence the explicit deletes.
WITH digest AS (
    DELETE FROM t_weekly_digests WHERE device_id = $1
), rules AS (
    DELETE FROM t_alert_rules WHERE device_id = $1
), events AS (
    DELETE FROM t_events WHERE device_id = $1
)
UPDATE t_devices SET
    deleted_at    = now(),
    apns_token    = NULL,
    apns_env      = NULL,
    push_status   = 'none',
    attest_key_id = NULL,
    prefs         = '{}'::jsonb
WHERE t_devices.id = $1 AND t_devices.deleted_at IS NULL;

-- name: TouchDevice :exec
UPDATE t_devices SET last_seen_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: MarkDevicePushUnregistered :exec
-- Called on APNs 410: token is dead, drop it and stop selecting the device
-- for pushes.
UPDATE t_devices
SET push_status = 'unregistered',
    push_status_changed_at = now(),
    apns_token = NULL,
    apns_env   = NULL
WHERE id = $1 AND deleted_at IS NULL;
