-- name: UpsertDevice :one
-- push_status tracks token state: providing apns_token marks the device
-- 'active'; omitting it keeps the current status/token pair intact.
-- Re-registering a soft-deleted device ("Delete my data") starts from a clean
-- slate: only the values in this request are kept, nothing from the old row
-- (locale, tz, version, prefs, push identifiers) comes back with it.
-- Callers must first release the APNs token from any other holder
-- (ReleaseAPNsToken) in the same transaction.
INSERT INTO t_devices (id, apns_token, apns_env, locale, tz, app_version, prefs,
                       push_status, push_status_changed_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        CASE WHEN $2::text IS NOT NULL THEN 'active' ELSE 'none' END,
        CASE WHEN $2::text IS NOT NULL THEN now() END,
        now())
ON CONFLICT (id) DO UPDATE SET
    apns_token   = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.apns_token
                        ELSE COALESCE(EXCLUDED.apns_token, t_devices.apns_token) END,
    apns_env     = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.apns_env
                        ELSE COALESCE(EXCLUDED.apns_env, t_devices.apns_env) END,
    locale       = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.locale
                        ELSE COALESCE(EXCLUDED.locale, t_devices.locale) END,
    tz           = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.tz
                        ELSE COALESCE(EXCLUDED.tz, t_devices.tz) END,
    app_version  = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.app_version
                        ELSE COALESCE(EXCLUDED.app_version, t_devices.app_version) END,
    prefs        = CASE WHEN t_devices.deleted_at IS NOT NULL THEN EXCLUDED.prefs
                        WHEN EXCLUDED.prefs = '{}'::jsonb THEN t_devices.prefs
                        ELSE EXCLUDED.prefs END,
    push_status  = CASE WHEN EXCLUDED.apns_token IS NOT NULL THEN 'active'
                        WHEN t_devices.deleted_at IS NOT NULL THEN 'none'
                        ELSE t_devices.push_status END,
    push_status_changed_at = CASE
        WHEN EXCLUDED.apns_token IS NOT NULL
             AND t_devices.apns_token IS DISTINCT FROM EXCLUDED.apns_token
        THEN now()
        WHEN t_devices.deleted_at IS NOT NULL THEN NULL
        ELSE t_devices.push_status_changed_at END,
    last_seen_at = now(),
    deleted_at   = NULL
RETURNING id;

-- name: ReleaseAPNsToken :exec
-- A token belongs to one install. When a new device id registers a token that
-- another live device holds (reinstall that lost its Keychain), the previous
-- holder is demoted: token/env cleared and push_status 'unregistered' (keeps
-- ck_t_devices_push_token_pair satisfied). Runs in the same transaction as
-- UpsertDevice so the partial unique index uq_t_devices_apns_token never trips.
UPDATE t_devices
SET apns_token = NULL,
    apns_env   = NULL,
    push_status = 'unregistered',
    push_status_changed_at = now()
WHERE apns_token = $1 AND id <> $2 AND deleted_at IS NULL;

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
-- the rules), analytics events (t_events, device_id-keyed), subscription links
-- (t_device_subscriptions), and the push token / attestation key / locale / tz /
-- app version / prefs on the device row.
-- Soft delete never fires the device FK cascades, hence the explicit deletes.
-- Spot reports, crowd reports and score feedback stay keyed by device_id on
-- purpose: they only feed anonymous aggregates and the device row carries no
-- personal data after this statement.
WITH digest AS (
    DELETE FROM t_weekly_digests WHERE device_id = $1
), rules AS (
    DELETE FROM t_alert_rules WHERE device_id = $1
), events AS (
    DELETE FROM t_events WHERE device_id = $1
), subs AS (
    DELETE FROM t_device_subscriptions WHERE device_id = $1
)
UPDATE t_devices SET
    deleted_at    = now(),
    apns_token    = NULL,
    apns_env      = NULL,
    push_status   = 'none',
    push_status_changed_at = NULL,
    locale        = NULL,
    tz            = NULL,
    app_version   = NULL,
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
