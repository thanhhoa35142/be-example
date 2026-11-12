-- Scoped API keys for machine/agent clients (MCP server, integrations).
-- Keys are shown once at creation; only the SHA-256 hash is stored.
-- Authorization is grant-based: api_key_grant rows list allow/deny rules
-- over (resource, action) pairs; evaluation is deny-wins.

CREATE TABLE api_key (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    key_prefix    text NOT NULL UNIQUE,          -- e.g. "phk_live_a1b2c3" for display/lookup
    key_hash      bytea NOT NULL UNIQUE,         -- sha256 of the full secret
    rate_limit_rps integer NOT NULL DEFAULT 60,
    expires_at    timestamptz,
    last_used_at  timestamptz,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX api_key_active_idx ON api_key (created_at DESC) WHERE revoked_at IS NULL;

CREATE TABLE api_key_grant (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    api_key_id uuid NOT NULL REFERENCES api_key(id) ON DELETE CASCADE,
    effect     text NOT NULL CHECK (effect IN ('allow', 'deny')),
    resource   text NOT NULL,   -- e.g. "spots", "recommendations", "*"
    action     text NOT NULL,   -- e.g. "read", "*"
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (api_key_id, effect, resource, action)
);

CREATE INDEX api_key_grant_key_idx ON api_key_grant (api_key_id);
