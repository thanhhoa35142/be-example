-- name: GetAdminUserByEmail :one
SELECT * FROM t_admin_users WHERE email = $1;

-- name: GetAdminUserByID :one
SELECT * FROM t_admin_users WHERE id = $1;

-- name: InsertAdminUser :one
INSERT INTO t_admin_users (email, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: ListAdminUsers :many
SELECT email, totp_confirmed_at, disabled_at, last_login_at
FROM t_admin_users
ORDER BY email;

-- name: TouchAdminUserLogin :exec
UPDATE t_admin_users SET last_login_at = now(), updated_at = now()
WHERE id = $1;

-- name: RecordAdminAuthFailure :exec
-- One counter for password and second-factor failures; at max_failures the
-- account locks for lockout_seconds. A still-locked account does not extend
-- its own lockout.
UPDATE t_admin_users
SET failed_attempts = failed_attempts + 1,
    locked_until = CASE WHEN failed_attempts + 1 >= sqlc.arg('max_failures')::int
                        AND (locked_until IS NULL OR locked_until <= now())
                        THEN now() + make_interval(secs => sqlc.arg('lockout_seconds')::float8)
                        ELSE locked_until END,
    updated_at = now()
WHERE id = $1;

-- name: ResetAdminAuthFailures :exec
UPDATE t_admin_users SET failed_attempts = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: ConfirmAdminUserTOTP :exec
UPDATE t_admin_users
SET totp_secret = $2, totp_confirmed_at = now(), updated_at = now()
WHERE id = $1;

-- name: ResetAdminUserTOTP :exec
-- Staff CLI reset-2fa: drop the secret, its confirmation, pending challenges,
-- recovery codes and replay counters so the next login re-enrolls.
UPDATE t_admin_users
SET totp_secret = NULL, totp_confirmed_at = NULL, updated_at = now()
WHERE id = $1;

-- name: UpdateAdminUserPassword :exec
-- Also clears the failure counter: a successful change (self-service or the
-- staff CLI) is a fresh start for the account.
UPDATE t_admin_users
SET password_hash = $2, failed_attempts = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: SetAdminUserDisabled :exec
UPDATE t_admin_users
SET disabled_at = CASE WHEN sqlc.arg('disabled')::boolean THEN now() ELSE NULL END, updated_at = now()
WHERE id = $1;

-- name: CreateAdminSession :one
INSERT INTO t_admin_sessions (token_hash, admin_user_id, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAdminSessionByTokenHash :one
SELECT s.*, u.email AS user_email, u.disabled_at AS user_disabled_at,
       u.totp_confirmed_at AS user_totp_confirmed_at
FROM t_admin_sessions s
JOIN t_admin_users u ON u.id = s.admin_user_id
WHERE s.token_hash = $1;

-- name: TouchAdminSession :exec
-- Throttled idle refresh: only writes when last_seen_at is older than a
-- minute, so ordinary reads stay read-only.
UPDATE t_admin_sessions SET last_seen_at = now()
WHERE id = $1 AND last_seen_at < now() - interval '60 seconds';

-- name: DeleteAdminSession :exec
DELETE FROM t_admin_sessions WHERE token_hash = $1;

-- name: ListAdminSessions :many
SELECT * FROM t_admin_sessions
WHERE admin_user_id = $1
ORDER BY last_seen_at DESC;

-- name: RevokeOtherAdminSessions :exec
-- keep_token_hash NULL revokes every session (used when disabling a user).
DELETE FROM t_admin_sessions
WHERE admin_user_id = $1
  AND (sqlc.narg('keep_token_hash')::bytea IS NULL OR token_hash <> sqlc.narg('keep_token_hash'));

-- name: DeleteExpiredAdminSessions :execrows
DELETE FROM t_admin_sessions WHERE expires_at < now() - interval '1 day';

-- name: InsertAdminRecoveryCodes :exec
INSERT INTO t_admin_recovery_codes (admin_user_id, code_hash)
SELECT $1, unnest(sqlc.arg('code_hashes')::bytea[]);

-- name: DeleteAdminRecoveryCodes :exec
DELETE FROM t_admin_recovery_codes WHERE admin_user_id = $1;

-- name: ConsumeAdminRecoveryCode :execrows
-- Marks the code used atomically; 0 rows = unknown or already used.
UPDATE t_admin_recovery_codes SET used_at = now()
WHERE admin_user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: TryInsertAdminTOTPStep :execrows
-- 0 rows = this counter was already accepted (replay).
INSERT INTO t_admin_totp_used (admin_user_id, step)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DeleteAdminTOTPUsedForUser :exec
DELETE FROM t_admin_totp_used WHERE admin_user_id = $1;

-- name: PruneAdminTOTPUsed :exec
DELETE FROM t_admin_totp_used WHERE used_at < now() - interval '10 minutes';
