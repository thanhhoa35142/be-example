---
name: verify
description: Verify backend changes end-to-end before considering work done. Boots Postgres, runs the API, and smoke-tests real endpoints. Use after touching handlers, modules, SQL queries, migrations, or worker jobs.
---

# Verify — Picnic backend

Goal: prove the change works against the real running system, not just `go build`.
The local `picnic` DB is the future prod dataset — keep it clean (no leftover test devices/packs).

## 1. Static checks (always)

```bash
cd pickhour-backend
go build ./...
make lint        # golangci-lint + gofmt
```

## 2. Bring up the system

```bash
make db-up       # Postgres+PostGIS via docker compose (port 4343)
# .env already exists; API runs migrations on boot.
go run ./cmd/api &        # PORT=8080 — or `make dev` if air is installed
```

Run the worker too (`go run ./cmd/worker &`) only when the change touches
jobs, scoring, ingest, weather, crowd, or notifier — most API changes don't need it.
Scores are precomputed by the worker; the API only reads them.

## 3. Smoke checklist (curl)

```bash
B=http://localhost:8080

curl -s $B/healthz            # {"status":"ok"}
curl -s $B/readyz             # 200 = DB reachable; 503 = DB down

# Public reads (bbox covers central Amsterdam — the seeded dataset)
curl -s "$B/v1/spots?bbox=4.85,52.35,4.90,52.37&hours=24" | head -c 2000
curl -s "$B/v1/packs" | head -c 1000
curl -s "$B/v1/recommendations" | head -c 1000
```

Then exercise the changed surface:

- `GET /v1/spots/{id}` and `GET /v1/spots/{id}/forecast` — take an `id` from the spots list.
- `GET /v1/weather/nearby?lat=..&lon=..` — check actual param names in `docs/API.md` / `docs/openapi.yaml` first.
- `GET /v1/getaways` — needs a device token (see below).

### Device-authenticated flow

`POST /v1/devices` returns `deviceSecret` exactly once; authenticated calls use
`X-Device-ID` + `X-Device-Token` headers (NOT `Authorization`).

```bash
ID=$(uuidgen | tr 'A-Z' 'a-z')
SECRET=$(curl -s -X POST $B/v1/devices -H 'content-type: application/json' \
  -d "{\"id\":\"$ID\",\"tz\":\"Europe/Amsterdam\"}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["deviceSecret"])')

# use it, e.g. alerts/reports/events endpoints that require deviceAuthHeader
curl -s "$B/v1/alerts" -H "X-Device-ID: $ID" -H "X-Device-Token: $SECRET"

# ALWAYS clean up — test devices must not stay in the picnic DB
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE $B/v1/devices/$ID \
  -H "X-Device-ID: $ID" -H "X-Device-Token: $SECRET"   # expect 204
```

## 4. Tests

```bash
# Unit-free suite: only integration/e2e tests remain.
# Integration tests need a scratch DB — locally that's picnic_test, never picnic:
TEST_DATABASE_URL='postgres://picnic:picnic@127.0.0.1:4343/picnic_test?sslmode=disable' \
  go test ./...
# Redis-gated tests skip unless REDIS_TEST_URL is set — that's expected.
```

## 5. Done criteria

- `go build`, `make lint`, `go test` all pass.
- API boots, `/readyz` is 200, and every endpoint touched by the diff returns
  the expected shape (camelCase JSON, reason codes — never translated strings).
- No test devices/packs left in `picnic`.
- If a check fails 3 rounds in a row, stop and escalate to a human instead of
  retrying or weakening the check.
