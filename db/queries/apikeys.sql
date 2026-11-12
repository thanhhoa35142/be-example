-- name: InsertAPIKey :one
INSERT INTO api_key (name, description, key_prefix, key_hash, rate_limit_rps, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, description, key_prefix, rate_limit_rps, expires_at, last_used_at, revoked_at, created_at;

-- name: GetAPIKeyByHash :one
SELECT id, name, description, key_prefix, key_hash, rate_limit_rps,
       expires_at, last_used_at, revoked_at, created_at
FROM api_key
WHERE key_hash = $1;

-- name: TouchAPIKeyLastUsed :exec
UPDATE api_key SET last_used_at = now() WHERE id = $1;

-- name: ListAPIKeys :many
SELECT id, name, description, key_prefix, rate_limit_rps,
       expires_at, last_used_at, revoked_at, created_at
FROM api_key
ORDER BY created_at DESC;

-- name: RevokeAPIKey :execrows
UPDATE api_key SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: InsertAPIKeyGrant :exec
INSERT INTO api_key_grant (api_key_id, effect, resource, action)
VALUES ($1, $2, $3, $4)
ON CONFLICT (api_key_id, effect, resource, action) DO NOTHING;

-- name: ListAPIKeyGrants :many
SELECT id, api_key_id, effect, resource, action, created_at
FROM api_key_grant
WHERE api_key_id = $1
ORDER BY created_at;

-- name: ListAPIKeyGrantsByKeyIDs :many
SELECT g.id, g.api_key_id, g.effect, g.resource, g.action, g.created_at
FROM api_key_grant g
JOIN api_key k ON k.id = g.api_key_id
WHERE k.key_hash = $1;

-- name: DeleteAPIKeyGrant :execrows
DELETE FROM api_key_grant
WHERE api_key_id = $1 AND effect = $2 AND resource = $3 AND action = $4;
