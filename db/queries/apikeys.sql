-- name: InsertAPIKey :one
INSERT INTO t_api_keys (name, description, key_prefix, key_hash, rate_limit_rps, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, description, key_prefix, rate_limit_rps, expires_at, last_used_at, revoked_at, created_at;

-- name: GetAPIKeyByHash :one
-- Auth lookup: only live keys (not revoked, not expired) resolve.
SELECT id, name, description, key_prefix, key_hash, rate_limit_rps,
       expires_at, last_used_at, revoked_at, created_at
FROM t_api_keys
WHERE key_hash = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: TouchAPIKeyLastUsed :exec
-- Throttled to at most one write per 5 minutes per key.
UPDATE t_api_keys SET last_used_at = now()
WHERE id = $1
  AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes');

-- name: ListAPIKeys :many
SELECT id, name, description, key_prefix, rate_limit_rps,
       expires_at, last_used_at, revoked_at, created_at
FROM t_api_keys
ORDER BY created_at DESC;

-- name: RevokeAPIKey :execrows
UPDATE t_api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: InsertAPIKeyGrant :exec
INSERT INTO t_api_key_grants (api_key_id, effect, resource, action)
VALUES ($1, $2, $3, $4)
ON CONFLICT (api_key_id, effect, resource, action) DO NOTHING;

-- name: ListAPIKeyGrants :many
SELECT id, api_key_id, effect, resource, action, created_at
FROM t_api_key_grants
WHERE api_key_id = $1
ORDER BY created_at;

-- name: ListAPIKeyGrantsByKeyIDs :many
SELECT g.id, g.api_key_id, g.effect, g.resource, g.action, g.created_at
FROM t_api_key_grants g
JOIN t_api_keys k ON k.id = g.api_key_id
WHERE k.key_hash = $1;

-- name: DeleteAPIKeyGrant :execrows
DELETE FROM t_api_key_grants
WHERE api_key_id = $1 AND effect = $2 AND resource = $3 AND action = $4;
