-- name: UpsertAdminGoogleToken :exec
INSERT INTO t_admin_google_tokens (admin_user_id, google_sub, google_email, refresh_token, scopes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (admin_user_id) DO UPDATE
SET google_sub = EXCLUDED.google_sub,
    google_email = EXCLUDED.google_email,
    refresh_token = EXCLUDED.refresh_token,
    scopes = EXCLUDED.scopes,
    updated_at = now();

-- name: GetAdminGoogleToken :one
SELECT admin_user_id, google_sub, google_email, refresh_token, scopes,
       gsc_site_url, ga4_property_id, created_at, updated_at
FROM t_admin_google_tokens
WHERE admin_user_id = $1;

-- name: DeleteAdminGoogleToken :exec
DELETE FROM t_admin_google_tokens WHERE admin_user_id = $1;
