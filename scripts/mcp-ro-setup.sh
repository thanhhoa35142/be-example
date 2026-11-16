#!/usr/bin/env bash
# Provision the mcp_ro read-only Postgres role used by the MCP `db_query`
# tool (see db/mcp-ro.sql). Idempotent — safe to re-run; it also refreshes
# the role password and rewrites MCP_RO_DATABASE_URL in .env.
#
# The admin connection (DATABASE_URL user, e.g. picnic) is only used to
# CREATE/GRANT the role — runtime traffic goes through mcp_ro, which has
# SELECT on an explicit table allowlist and default_transaction_read_only.
set -euo pipefail
cd "$(dirname "$0")/.."

upsert_env() { # upsert_env KEY VALUE — replace or append KEY=VALUE in .env
    local key="$1" val="$2" tmp
    tmp="$(mktemp)"
    if grep -q "^${key}=" .env; then
        sed "s|^${key}=.*|${key}=${val}|" .env > "$tmp"
    else
        cat .env > "$tmp"; printf '%s=%s\n' "$key" "$val" >> "$tmp"
    fi
    cat "$tmp" > .env && rm -f "$tmp"
}

ENV_FILE=.env
[ -f "$ENV_FILE" ] || { echo "error: $ENV_FILE not found" >&2; exit 1; }
# .env is plain KEY=value — safe to source.
set -a; . "./$ENV_FILE"; set +a

: "${DATABASE_URL:?DATABASE_URL missing in $ENV_FILE}"

# --- ensure password + derived DSN in .env -------------------------------
if [ -z "${MCP_RO_PASSWORD:-}" ]; then
    MCP_RO_PASSWORD="$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | base64 | tr -d '=+/')"
fi

# postgres://user:pass@host:port/db?params  ->  same but mcp_ro credentials
MCP_RO_DATABASE_URL="$(printf '%s' "$DATABASE_URL" | sed -E "s|^([a-z]+://)[^:/@]+:[^@]*@|\1mcp_ro:${MCP_RO_PASSWORD}@|")"
if printf '%s' "$MCP_RO_DATABASE_URL" | grep -q 'mcp_ro:'; then
    upsert_env MCP_RO_PASSWORD "$MCP_RO_PASSWORD"
    upsert_env MCP_RO_DATABASE_URL "$MCP_RO_DATABASE_URL"
else
    echo "error: could not derive MCP_RO_DATABASE_URL from DATABASE_URL" >&2
    exit 1
fi

# --- wait for the db container --------------------------------------------
docker compose up -d db >/dev/null
for i in $(seq 1 30); do
    docker compose exec -T db pg_isready -q && break
    [ "$i" = 30 ] && { echo "error: db not ready" >&2; exit 1; }
    sleep 1
done

# --- apply db/mcp-ro.sql ---------------------------------------------------
# Inside the container, reach postgres on localhost with the same
# credentials as DATABASE_URL.
ADMIN_URL="$(printf '%s' "$DATABASE_URL" | sed -E 's|@[^/@]+/|@127.0.0.1:5432/|')"
docker compose exec -T db psql "$ADMIN_URL" \
    -v ro_password="$MCP_RO_PASSWORD" < db/mcp-ro.sql

echo "mcp_ro ready. Set in $ENV_FILE:"
echo "  MCP_RO_DATABASE_URL=$MCP_RO_DATABASE_URL"
