-- name: UpsertDevice :one
INSERT INTO device (id, apns_token, apns_env, locale, tz, app_version, prefs, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (id) DO UPDATE SET
    apns_token   = COALESCE(EXCLUDED.apns_token, device.apns_token),
    apns_env     = COALESCE(EXCLUDED.apns_env, device.apns_env),
    locale       = COALESCE(EXCLUDED.locale, device.locale),
    tz           = COALESCE(EXCLUDED.tz, device.tz),
    app_version  = COALESCE(EXCLUDED.app_version, device.app_version),
    prefs        = CASE WHEN EXCLUDED.prefs = '{}'::jsonb THEN device.prefs ELSE EXCLUDED.prefs END,
    last_seen_at = now(),
    deleted_at   = NULL
RETURNING id;

-- name: GetActiveDevice :one
SELECT id FROM device
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetDeviceTz :one
SELECT COALESCE(tz, 'UTC') FROM device
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteDevice :execrows
UPDATE device SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: TouchDevice :exec
UPDATE device SET last_seen_at = now()
WHERE id = $1 AND deleted_at IS NULL;
