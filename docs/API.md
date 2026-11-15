# Pickhour API — Client Contract (v1)

Base URL: `https://api.pickhour.app` (dev: `http://localhost:8080`)

All timestamps are **RFC3339 in UTC** (`2026-11-11T14:00:00Z`).
All distances in **meters**, coordinates in **WGS84 decimal degrees**.
`coordinates` arrays are **GeoJSON order: `[longitude, latitude]`**.

---

## Conventions

### Success envelope

Every non-GeoJSON success response is wrapped:

```json
{ "data": { ... } }
```

### Error envelope (RFC 7807 `application/problem+json`)

```json
{
  "type": "about:blank",
  "code": "not_found",
  "title": "Spot not found",
  "status": 404,
  "detail": "optional human-readable detail",
  "instance": "/v1/spots/{id}",
  "requestId": "3f6569db-3d46-41c5-a1dd-11335502dcce"
}
```

| Field | Meaning |
|---|---|
| `code` | **Machine-readable. Switch on this, never on `title`.** |
| `requestId` | Server-generated (or echoes your `X-Request-ID` header when it is 1-64 chars of `[A-Za-z0-9._-]`; anything else is replaced). Show it in error UI / send to support. |
| `detail` | Optional, may be shown to the user. |

### Error codes

| code | HTTP | Meaning |
|---|---|---|
| `bad_request` | 400 | Generic bad request |
| `invalid_id` | 400 | Path/body ID is not a UUID |
| `invalid_bbox` | 400 | `bbox` malformed or out of WGS84 bounds |
| `bbox_too_large` | 400 | Viewport > ~0.5° — zoom in |
| `invalid_when` | 400 | `when` / `from` is not RFC3339 |
| `invalid_query` | 400 | lat/lon/radius/limit out of range; on `/v1/spots`: both or neither of `bbox`/`ids`, bad/too many `ids`, `hours` outside 1–24, unknown `include` |
| `invalid_body` | 400 | JSON body malformed or missing fields |
| `invalid_report` | 400 | report type/level/verdict out of domain; `type=issue` without a valid `issueCode`; score-feedback `tsHour` not on the hour or outside the allowed window |
| `unauthorized` | 401 | admin surface: missing/invalid bearer token |
| `account_mismatch` | 403 | `POST /v1/entitlement/link`: the transaction's `appAccountToken` is a UUID naming a different device |
| `not_found` | 404 | spot or device does not exist |
| `merged_spot` | 301 | spot merged — follow `Location` header |
| `conflict` | 409 | only a lost race between concurrent registrations of the same `apnsToken` — retry |
| `device_limit` | 409 | `POST /v1/entitlement/link`: the subscription is already linked to the maximum number of active devices (5) |
| `payload_too_large` | 413 | request body over the size cap (64 KiB default; `POST /v1/events` 512 KiB; App Store webhook 256 KiB) |
| `rate_limited` | 429 | too many requests (per-IP limit, carries `Retry-After`) or too many reports/feedback per device per 24h |
| `service_unavailable` | 503 | server-side App Store verification is not configured — retry later |
| `timeout` | 503 | DB statement/lock timeout exceeded — transient, safe to retry (carries `Retry-After`) |
| `internal_error` | 500 | unexpected — details only in server logs via `requestId` |

### Request headers

- `X-Request-ID: <id>` (optional) — sent for support correlation; echoed in responses if 1-64 chars of `[A-Za-z0-9._-]`, otherwise the server generates one.
- `X-Device-ID: <uuid>` — the device id on routes that have no `:id` path parameter (`/v1/alerts*`, reports, `/v1/events`, `/v1/entitlement/link`).
- `X-Device-Token: <secret>` — the per-device credential issued by `POST /v1/devices` (returned once as `deviceSecret`, kept in the iOS Keychain). **Required on every device-scoped route** together with `X-Device-ID` (or the `:id` path parameter for `/v1/devices/{id}/*`). Missing/invalid → `401 unauthorized`. It is deliberately not `Authorization`: arbitrary Authorization headers must not interact with the public response cache.
- `X-Client: pickhour-ios` — identifies the app; sent by the official client.
- `X-Client-Version: <semver>+<build>` — app version, e.g. `0.1.0+3`.
- `X-Client-OS: ios <version>` — OS version string.
- `Content-Type: application/json` on POST/PUT/DELETE.

### Rate limits and size limits

- **Per client IP**, in-memory token buckets (defaults, configurable by env): reads (`GET`) 600/min burst 120; writes (`POST/PUT/PATCH/DELETE`) 60/min burst 30; `POST /v1/devices` 30/hour burst 10. `/healthz` and `/readyz` are exempt. Over budget: `429 rate_limited` with a `Retry-After` header (seconds) — back off for at least that long.
- **Per device**, rolling 24h: 30 crowd reports, 30 spot reports, 100 score-feedback submissions, 5000 analytics events (`429 rate_limited`).
- **Request bodies** are capped (`413 payload_too_large`): 64 KiB by default, 512 KiB for `POST /v1/events`, 256 KiB for `POST /v1/appstore/notifications`.

---

## Endpoints

### `GET /healthz` / `GET /readyz`

Liveness/readiness probes. No auth.

```json
{ "status": "ok" }
```

---

### Response cache and compression (all public GETs)

`/v1/spots`, `/v1/spots/{id}`, `/v1/spots/{id}/forecast`, `/v1/packs` and `/v1/recommendations` are cached server-side. The `X-Cache` response header says what happened: `HIT` (served from the cache), `MISS` (computed and stored), `BYPASS` (computed, not cacheable: non-200, non-JSON, > 1 MiB), `COALESCED` (answered from a concurrent identical request). Requests with an `Authorization` header and non-GET requests are never cached; the header is absent when the cache is disabled or unavailable. JSON bodies of at least 1 KiB are gzip-compressed for clients sending `Accept-Encoding: gzip` (`Content-Encoding: gzip`, `Vary: Accept-Encoding`); smaller bodies are sent as is.

---

### `GET /v1/spots?bbox=&when=` (also `ids=`, `from=&hours=`, `include=area`)

Spots inside a viewport — or a batch of known spots (Saved screen) — with their score at a given hour.
**Returns raw GeoJSON `FeatureCollection` (NOT wrapped in `data`)** — feed directly to a GeoJSON decoder / map layer.

Query params (exactly one of `bbox` / `ids`):

| param | required | format | notes |
|---|---|---|---|
| `bbox` | one of | `west,south,east,north` | WGS84 degrees; each axis ≤ 0.5°. Malformed → `invalid_bbox` |
| `ids` | one of | `uuid,uuid,...` | 1–50 spot ids. Malformed uuid / >50 → `invalid_query`. Unknown or non-active ids are omitted; a merged id is resolved to its merge target (so `properties.id` may differ from the id you asked for — update your stored id). Duplicates collapse |
| `when` | no | RFC3339 | hour for `properties.score`; truncated to the hour server-side; default = `from`, else now |
| `from` | no | RFC3339 | start of the hourly series (truncated to the hour); default = now. Malformed → `invalid_when` |
| `hours` | no | int 1–24 | adds `properties.scores` (below); out of range → `invalid_query`. Without it `scores` is absent |
| `include` | no | `area` | comma-separated; adds `properties.area`. Unknown value → `invalid_query` |
| `kinds` | no | `park,beach,site,table` | comma list; keep spots whose kind is in the list. Unknown value → `invalid_query` |
| `amenities` | no | `toilet,water,bbq,playground,shelter,parking,bicycle_parking,dog_area` | comma list; keep spots that have **every** listed amenity (AND). Unknown value → `invalid_query` |
| `minScore` | no | int 0–100 | keep spots whose score at the requested hour (`when`, default now) is ≥ `minScore`; spots with no score at that hour are excluded. Out of range / not an int → `invalid_query` |

Both or neither of `bbox` / `ids` → `400 invalid_query`. `hours` works with either selector. The `ids` variant has no viewport size cap.

**Wide viewports are capped.** The server snaps `bbox` outward to a 0.02° grid. When the snapped box is wider or taller than **0.12°** it returns only the **top N spots by score at the requested hour** (best first; spots without a score last; ties by id, so the result is deterministic) instead of every spot, which keeps zoomed-out responses small and cacheable. Boxes up to 0.12° and the `ids` variant always return everything. N depends on how heavy each feature is: `floor(655360 / (520 + 64·hours + 370·[include=area]))`, min 100 — e.g. 1260 with no extras, 508 with `hours=12`, 270 with `hours=24&include=area`. When the cap cut spots the response gains two **top-level** members next to `features`: `"truncated": true` and `"total": <spots matching before the cap>`. Both are absent when nothing was cut, so existing clients that ignore unknown members are unaffected. Filters (`kinds`, `amenities`, `minScore`) apply before the cap and `total` counts only spots that passed them. Features are now ordered by score (descending) rather than arbitrarily.

The `kinds` / `amenities` / `minScore` filters apply to both selectors (for `ids`, to the resolved target); absent or empty = no filter. Filtering happens in SQL, so the response shape is unchanged — filtered-out spots are simply omitted.

```json
{
  "type": "FeatureCollection",
  "features": [
    {
      "type": "Feature",
      "geometry": { "type": "Point", "coordinates": [4.8686, 52.3579] },
      "properties": {
        "id": "01a1cad6-366a-7ab3-a298-6d9f9f28f665",
        "kind": "park",
        "name": "Vondelpark",
        "names": { "name": "Vondelpark", "en": "Vondelpark", "nl": "Vondelpark", "de": "Vondelpark" },
        "country": "NL",
        "city": "Amsterdam",
        "tz": "Europe/Amsterdam",
        "lastVerifiedAt": "2026-11-11T13:39:58Z",
        "when": "2026-11-11T14:00:00Z",
        "score": {
          "score": 62,
          "confidence": 2,
          "capReason": null,
          "reasons": [
            { "code": "weather.clear", "params": { "temp": 21 } },
            { "code": "crowd.low" }
          ],
          "computedAt": "2026-11-11T13:00:00Z"
        }
      }
    }
  ]
}
```

| property | type | notes |
|---|---|---|
| `kind` | string | `park` \| `beach` \| `site` \| `table` |
| `names` | object | lang → name; pick by device locale, fallback `name` key |
| `score` | object \| null | `null` when no score computed yet — still render the pin |
| `score.score` | int 0–100 | overall picnic score |
| `score.confidence` | int 1–3 | 3 = high; model-only crowd caps at 2 |
| `score.capReason` | string \| null | e.g. `rain`, `heat`, `cold` when hard-capped |
| `score.reasons` | array ≤3 | `{code, params?}` — **translate client-side** |
| `scores` | array | only with `hours`: `{tsHour, score, capReason}` for `hours` consecutive hours from `from` (truncated to the hour), ascending; hours without a computed score are omitted; `[]` if none. `capReason` is `null` or e.g. `rain`, `dark`. Built in one query — use it for the map time-lapse instead of one request per hour |
| `truncated` / `total` (top level, not in `properties`) | bool / int | only when a wide viewport was capped, see above |
| `area` | GeoJSON MultiPolygon \| null | only with `include=area`: park outline simplified server-side (~5 m, `ST_SimplifyPreserveTopology`, coordinates rounded to 6 decimals); `null` for point spots. `geometry` stays the Point |

Time-lapse / outline example (`GET /v1/spots?bbox=4.85,52.35,4.90,52.37&from=2026-11-13T06:00:00Z&hours=3&include=area`), one feature's extra properties:

```json
"area": { "type": "MultiPolygon", "coordinates": [[[[4.855018, 52.356319], [4.855804, 52.354848], "..."]]] },
"scores": [
  { "tsHour": "2026-11-13T06:00:00Z", "score": 56, "capReason": null },
  { "tsHour": "2026-11-13T07:00:00Z", "score": 55, "capReason": null },
  { "tsHour": "2026-11-13T08:00:00Z", "score": 54, "capReason": null }
]
```

---

### `GET /v1/spots/{id}`

Spot detail.

```json
{
  "data": {
    "id": "01a1cad6-366a-7ab3-a298-6d9f9f28f665",
    "kind": "park",
    "name": "Vondelpark",
    "names": { "en": "Vondelpark", "nl": "Vondelpark" },
    "coordinates": [4.8686, 52.3579],
    "area": { "type": "MultiPolygon", "coordinates": [ ... ] },
    "country": "NL",
    "city": "Amsterdam",
    "tz": "Europe/Amsterdam",
    "lastVerifiedAt": "2026-11-11T13:39:58Z",
    "amenities": [
      { "type": "toilet", "distanceM": 120, "source": "osm" },
      { "type": "parking", "distanceM": 300, "source": "osm" }
    ],
    "sources": [
      { "source": "osm", "sourceId": "node/12345", "version": 1, "importedAt": "2026-11-11T13:39:58Z" }
    ],
    "weatherSource": "openmeteo"
  }
}
```

| field | notes |
|---|---|
| `coordinates` | `[lon, lat]` — for MapKit use `CLLocationCoordinate2D(latitude: c[1], longitude: c[0])` |
| `area` | optional GeoJSON MultiPolygon (park outline); absent for point spots |
| `amenities[].type` | `toilet`, `parking`, `water`, `bbq`, `playground`, `dog_area`, ... |
| `lastVerifiedAt` | show as "data verified …" in UI |
| `weatherSource` | `"weatherkit"` \| `"openmeteo"` \| `null` — provider of the forecast behind this spot's scores (null = no weather yet). **Attribution is required**: render "Apple Weather" (link to https://weatherkit.apple.com/legal-attribution.html) or "Open-Meteo" (link to https://open-meteo.com/) |

Merged spot → `301` with `Location: /v1/spots/{newId}` — follow it.

---

### Share page and universal links (outside `/v1`)

| route | notes |
|---|---|
| `GET /s/{spotId}` | Public HTML page for a shared spot: name, current score, "Open in Pickhour" (`pickhour://spot/{id}`), optional "Get the App", Apple Maps directions. `301` to the new id for a merged spot, `404` page for an unknown one. The app shares `https://<share host>/s/{spotId}`; the host must route `/s/*` and `/.well-known/*` to this API. Optional `?at=<RFC3339>` (a planned picnic, see below) |
| `GET /.well-known/apple-app-site-association` | JSON claiming `/s/*` for the app ids in `APPLE_APP_IDS` (`<TeamID>.<bundle id>`, comma-separated). `404` while that variable is empty. Must be served over HTTPS without redirects |

`APPSTORE_APP_ID` (numeric) adds the "Get the App" link and the Safari smart banner.

**Planned time: `GET /s/{spotId}?at=<RFC3339>`.** The page then shows one hour of one spot: the planned date and time in the spot's timezone, the score for that hour and up to 3 reasons (plus "Open Pickhour to see better times" when the score is below 60 or capped). No hourly chart, other days or alternatives. Open Graph title becomes `Picnic on <Weekday D Mon, HH:MM> · <spot>`.

- **Canonical form** is the UTC hour: `at=2026-11-21T15:00:00Z`. Clients should build exactly this. A valid but non-canonical value (minutes, seconds, fraction, offset) gets a `302` (`Cache-Control: private`) to the canonical URL. Do not use an offset such as `+01:00`: an unencoded `+` in a query decodes as a space and the value is then ignored.
- **Valid range:** from the current UTC hour up to (not including) now + 7 days. A value that is unparsable, longer than 40 bytes, in the past or beyond that range is ignored: the plain spot page renders with `200` (never `4xx`/`5xx`) and `Cache-Control: private`.
- Any request carrying `at` gets `<meta name="robots" content="noindex">` and `X-Robots-Tag: noindex`. Valid `at` pages are `Cache-Control: public, max-age=300` with `<link rel="canonical">`.
- "Open in Pickhour" becomes `pickhour://spot/{id}?at=2026-11-21T15%3A00%3A00Z` (query-encoded; decode with `URLComponents.queryItems`).
- Merged spot: `301` to `/s/{newId}?at=<canonical>` (an invalid `at` is dropped).
- If no score exists for that hour (e.g. scoring has not run), the page shows place and time without a score.

### `GET /v1/spots/{id}/forecast`

Hourly scores for the next **7 days** (168 points).

```json
{
  "data": [
    {
      "tsHour": "2026-11-11T14:00:00Z",
      "score": 0,
      "components": { "weather": 32, "crowd": 44, "amenity": 37, "fresh": 100 },
      "capReason": "rain",
      "reasons": [
        { "code": "cap.rain" },
        { "code": "weather.cold", "params": { "temp": 2.8 } },
        { "code": "crowd.moderate" }
      ],
      "confidence": 2,
      "weather": { "tempC": 2.8, "feelsLikeC": -0.5, "precipProb": 80, "windKmh": 14.2, "uv": 0.4, "cloud": 95 }
    }
  ]
}
```

| field | notes |
|---|---|
| `components` | per-component 0–100; absent key = missing data (don't render as 0) |
| `weather` | raw provider forecast for the hour, metric units (°C, km/h, %); the object is absent when the hour has no weather row, and each key is absent when the provider had no value. The client converts units for display |
| `capReason` | `rain` \| `heat` \| `cold` \| `dark` \| `twilight` — score was hard-capped |
| `reasons` | max 3 codes; `params` optional numbers for interpolation |

---

### `GET /v1/packs`

City packs the app can use (status `beta` or `live`; drafts/retired are hidden), ordered by country then name. Served with `Cache-Control: public, max-age=3600`.

```json
{"data":{"items":[
  {"slug":"amsterdam","name":"Amsterdam","country":"NL","tz":"Europe/Amsterdam",
   "center":[4.89,52.37],"bbox":[4.73,52.28,5.07,52.43],"activeSpots":662}
]}}
```

| field | notes |
|---|---|
| `center` | `[lon, lat]` of a point guaranteed inside the pack boundary |
| `bbox` | `[west, south, east, north]` of the boundary envelope (WGS84, 4 decimals) |
| `activeSpots` | spots with status `active` |

---

### `GET /v1/recommendations?lat=&lon=&radius=&when=&limit=` (also `kinds=`, `amenities=`, `minScore=`)

Best spots around a point, ranked by score.

| param | required | default | range |
|---|---|---|---|
| `lat`, `lon` | yes | — | WGS84 |
| `radius` | no | 5000 | ≤ 50000 m |
| `when` | no | now | RFC3339, quantized to hour |
| `limit` | no | 3 (Free tier) | ≤ 50 |
| `kinds` | no | `park,beach,site,table` | comma list; keep spots whose kind is in the list. Unknown value → `invalid_query` |
| `amenities` | no | `toilet,water,bbq,playground,shelter,parking,bicycle_parking,dog_area` | comma list; keep spots that have **every** listed amenity (AND). Unknown value → `invalid_query` |
| `minScore` | no | int 0–100 | keep spots whose score at `when` is ≥ `minScore`; spots with no score at that hour are excluded. Out of range / not an int → `invalid_query` |

Filters are applied in SQL **before** `limit`, so `limit=3&kinds=park` returns the 3 best matching parks when they exist. Absent or empty = no filter.

```json
{
  "data": {
    "when": "2026-11-11T14:00:00Z",
    "items": [
      {
        "id": "01a1cad6-366a-7ab3-a298-6d9f9f28f665",
        "kind": "park",
        "name": "Vondelpark",
        "names": { "en": "Vondelpark" },
        "coordinates": [4.8686, 52.3579],
        "country": "NL",
        "city": "Amsterdam",
        "tz": "Europe/Amsterdam",
        "lastVerifiedAt": "2026-11-11T13:39:58Z",
        "distanceM": 1984.4,
        "score": {
          "score": 0,
          "confidence": 2,
          "capReason": "rain",
          "reasons": [ { "code": "cap.rain" }, { "code": "weather.cold", "params": { "temp": 7.1 } }, { "code": "crowd.high" } ],
          "computedAt": "2026-11-11T13:59:46Z"
        }
      }
    ]
  }
}
```

Ordering: `score` DESC (nulls last) → `distanceM` ASC.

---

### `POST /v1/devices`

Register or refresh a device. **`id` is generated client-side once (UUID) and stored in Keychain.**

Request:

```json
{
  "id": "3f6569db-3d46-41c5-a1dd-11335502dcce",
  "apnsToken": "abc123...",
  "apnsEnv": "sandbox",
  "locale": "en",
  "tz": "Europe/Amsterdam",
  "appVersion": "0.1.0",
  "prefs": { "tempUnit": "celsius", "distUnit": "km" }
}
```

| field | required | notes |
|---|---|---|
| `id` | yes | UUID, stable per install |
| `apnsToken` | no | send after APNs registration; `apnsEnv` = `sandbox`\|`production` |
| `tz` | no | IANA name (validated; `Local` rejected, at most 64 chars) — used to derive local dow/hour for crowd reports |
| `locale` | no | language tag, `[A-Za-z0-9_-]`, at most 35 chars |
| `appVersion` | no | `[A-Za-z0-9._+-]`, at most 32 chars |
| `apnsToken` | no | hex, 32-200 chars |
| `prefs` | no | free-form jsonb for user settings, at most 4096 bytes serialized |

Empty strings are treated as absent. A violated limit answers `400 invalid_body` and the `detail` names the field.

Response `200`:

```json
{ "data": { "id": "3f6569db-3d46-41c5-a1dd-11335502dcce", "deviceSecret": "<43-char base64url>" } }
```

`deviceSecret` is present **only when this call issued the credential** (first registration, or a pre-migration row with no credential). It is shown exactly once — store it in the Keychain and send it as `X-Device-Token` on every device-scoped request. Re-registering a device that already holds a credential requires the token in `X-Device-Token` (`401 unauthorized` otherwise) and never returns a new secret. Only the SHA-256 hash is stored server-side.

An `apnsToken` belongs to one install: if another live device holds it (e.g. a reinstall that lost its Keychain registers a new `id` with the same token), the **new device takes the token over**; the previous holder's token/env are cleared and its push status becomes `unregistered`. There is no longer a permanent `409` for this case (`409 conflict` only on a lost concurrent race — retry).

Re-registering an `id` that was deleted via `DELETE /v1/devices/{id}` works but starts from a clean slate: only the fields in the new request are stored.

### `DELETE /v1/devices/{id}`

Soft-delete (GDPR/CCPA unlink). In the same statement it hard-deletes the device's alert rules (their delivery log cascades), weekend digest, analytics events (`t_events`) and subscription links (so Plus is lost until the purchase is restored on a device), and clears the stored push token, attestation key, locale, tz, app version and prefs. Crowd reports, spot reports and score feedback are kept on purpose (they only feed anonymous aggregates; the device row no longer holds personal data). `204` on success, `404` if unknown/already deleted.
Requires `X-Device-Token` for that device id.

---

### `POST /v1/spots/{id}/reports`

Confirm a spot or report it wrong. One report per device/spot/type/**day** — resubmitting same day returns `duplicate` (not an error).

Request:

```json
{ "type": "issue", "issueCode": "no_toilet" }
```

| field | values |
|---|---|
| `type` | `confirm` \| `missing` \| `closed` \| `issue` |
| `issueCode` | **required** with `issue` (otherwise `invalid_report`): short machine code, `[A-Za-z0-9._-]`, 1-64 chars; ignored for other types |

Response `201` (or `200` when duplicate):

```json
{ "data": { "status": "accepted" } }
{ "data": { "status": "duplicate" } }
```

`429 rate_limited` after 30 spot reports/device/24h. `404 not_found` for an unknown/inactive spot.

---

### `POST /v1/spots/{id}/crowd-reports`

"Busy / moderate / quiet" vote for the **current** hour slot. Proximity is verified **on-device** — the app must only call this when the user is near the spot; the server never receives coordinates.

Request:

```json
{ "level": 3 }
```

`level`: `1` = quiet, `2` = moderate, `3` = busy.

Response `201`: `{ "data": { "status": "accepted" } }`
`429 rate_limited` after 30 reports/device/24h.

The community crowd level for a slot only overrides the model once **5 distinct devices** have voted on it (30-day window); each device counts once.

---

### `POST /v1/scores/feedback`

"Is this score right?" verdict on a spot+hour.

```json
{ "spotId": "01a1...", "tsHour": "2026-11-11T14:00:00Z", "verdict": 1 }
```

`verdict`: `-1` (too low) \| `0` (about right) \| `+1` (too high).

`tsHour` must be on the hour (as returned in forecast points) and within 72 h in the past to 7 days + 1 h in the future, else `400 invalid_report`. `404 not_found` for an unknown/inactive spot. `429 rate_limited` after 100 submissions/device/24h.

Response `201`: `{ "data": { "status": "accepted" } }`

---

## Reason codes (translate client-side)

Servers send codes + params; the app renders localized strings.

| code | params | meaning |
|---|---|---|
| `cap.rain` / `cap.heat` / `cap.cold` / `cap.dark` / `cap.twilight` | — | score hard-capped (emitted first when a cap applies). `cap.dark`: sun more than 6° below the horizon at mid-hour (cap 10); `cap.twilight`: civil twilight, sun -6°..0° (cap 40). Lowest applicable cap wins |
| `weather.rain_risk` | `prob` (0–100) | rain expected |
| `weather.hot` / `weather.cold` | `temp` (°C, 0.1) | extreme temperature |
| `weather.clear` | `temp` (°C, 0.1) | clear skies |
| `crowd.low` / `crowd.moderate` / `crowd.high` | — | busyness level |
| `amenity.well_equipped` | — | good facilities nearby |
| `fresh.recent` | — | data recently verified |

Unknown codes may appear as the scorer evolves — clients should render a sensible fallback, not crash. Source of truth: `backend/internal/modules/scoring/scorer.go`.

## Score components

`weather` 40% · `crowd` 25% · `amenity` 20% · `shade` 10% · `fresh` 5% (weights renormalize when a component is missing). `shade` has no data source yet — expect it absent.

---

## Alerts

Alert rules fire a push when the best score in an area meets `minScore` during a local-time window. Free plan: **1 rule**; Plus: **20 rules**. Windows may cross midnight (`startLocal` > `endLocal`). `daysMask` bit `i` = weekday `i` (0=Sunday … 6=Saturday); e.g. `127` = every day, `65` = Sat+Sun (`64+1`). Each (rule, window) delivers at most once.

### `GET /v1/alerts`

Send the device id as `X-Device-ID: <uuid>` together with `X-Device-Token`.

```json
{"data":{"items":[{"id":"...","deviceId":"...","spotId":"...","h3Cell":null,"areaCenter":null,"radiusM":null,"minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam","enabled":true}]}}
```

### `POST /v1/alerts` — create (201)

```json
{"spotId":"...","minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam"}
```

Target: exactly one of `spotId`, `h3Cell` or `area` (anything else → `400 invalid_body`). `h3Cell` and `area` also need `radiusM` (meters, 1..50000); `radiusM` with `spotId` is rejected.

Coarse area (no H3 library needed on the client):

```json
{"area":{"lat":52.36,"lon":4.88},"radiusM":3000,"minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam"}
```

`lat` ∈ [-90, 90], `lon` ∈ [-180, 180], both required. The server rounds both to 2 decimals (~1 km), converts the point to an H3 cell at **resolution 7** (`geo.H3Resolution`, ~1.2 km edge, same as `spot.cell_id`) and stores **only the cell** — the raw coordinates are never stored or logged and are not echoed back. Evaluation measures `radiusM` from the cell **center**, which can sit up to ~1.2 km from the submitted point.

Responses (list/create/update) always keep `h3Cell` and add `areaCenter`: `{"lat":52.359,"lon":4.88}` (cell center, 3 decimals) for area rules (`h3Cell` or `area` input), and `null` for spot rules. An area-rule response looks like:

```json
{"data":{"id":"...","deviceId":"...","spotId":null,"h3Cell":608432992271990783,"areaCenter":{"lat":52.359,"lon":4.88},"radiusM":3000,"minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam","enabled":true}}
```

Area rules count toward the Free plan limit (1 rule total). When an area rule fires, the push names the best-scoring active spot within `radiusM` of the cell center at that hour (ties: nearest, then id); payload `spotId` is that spot. Errors: `invalid_body` (bad shape/range), `plan_limit` (403, Free cap — enforced atomically, concurrent requests cannot exceed it), `not_found` (unknown device, or `spotId` does not reference an active spot).

Validation: `startLocal`/`endLocal` must be `HH:MM` (00:00-23:59) and must differ (overnight windows with `startLocal` > `endLocal` are valid); `tz` must be an IANA zone (empty and `Local` are rejected).

### `PUT /v1/alerts/{id}` / `DELETE /v1/alerts/{id}`

Same body as create (PUT replaces all fields). The device is identified by `X-Device-ID` + `X-Device-Token`; → `204`.

### Push payload (APNs)

When a rule fires, the notifier sends an APNs alert (`apns-push-type: alert`, priority 10, collapse-id = rule ID) to the device's `apnsToken` on `apnsEnv`. Request body:

```json
{
  "aps": {"alert": {"title-loc-key": "SPOT_NAMED", "title-loc-args": ["Vondelpark"], "loc-key": "ALERT_WINDOW", "loc-args": ["87"]}, "sound": "default"},
  "kind": "alert.window",
  "spotId": "<uuid>",
  "ruleId": "<uuid>",
  "score": 87,
  "windowStart": "2026-11-20T14:00:00Z"
}
```

- `aps.alert` carries **localization keys only** (`title-loc-key`, `loc-key`, optional `subtitle-loc-key`) and language-neutral `*-loc-args`; the app's Localizable strings hold every sentence and the server writes no text in any language. Key catalog, English reference text and full payloads per kind: [`PUSH_KEYS.md`](PUSH_KEYS.md).
- Custom keys are top-level next to `aps` (APNs requirement) and surface in iOS `userInfo`.
- The app routes taps on `kind == "alert.window"` + `spotId` to spot detail. Unknown `kind` values must be ignored.
- Devices without `apnsToken` are skipped (delivery stays unrecorded, not a failure).
- Credentials: `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_KEY` (.p8 PEM), optional `APNS_BUNDLE_ID`. Without them the worker logs payloads instead of sending.

## Weekend digest (Plus)

Plus devices can opt into one push per week (default **Thursday 18:00 device-local**, from `devices.tz`) naming the single best picnic window of the coming weekend across the spots they chose. Free devices cannot create a digest (`plan_limit`); a digest whose device later drops to Free is skipped silently. Device identification is the same as `/v1/alerts`: the device UUID in the path is the capability token.

### `PUT /v1/devices/{id}/digest` — create / replace

```json
{"spotIds":["<uuid>","<uuid>"],"enabled":true}
```

→ `200`

```json
{"data":{"spotIds":["<uuid>","<uuid>"],"skippedIds":[],"enabled":true,"sendDow":4,"sendHour":18}}
```

`skippedIds` appears on the `PUT` response only (not on `GET`) and is **always present**: an array of the requested ids that were dropped because the spot is unknown or inactive (request order, duplicates collapse), `[]` when nothing was dropped. A removed spot therefore never fails the update; the app should drop `skippedIds` from its saved list.

- `spotIds`: 1–20 spot UUIDs (an empty list is only accepted with `enabled:false`; after unknown/inactive ids are dropped the kept list must still be non-empty when `enabled:true`). Merged spot ids are resolved to their merge target like `GET /v1/spots?ids=`; duplicates collapse; the response carries the resolved ids in request order.
- `sendDow` is `time.Weekday` (0 = Sunday), `sendHour` is device-local 0–23. Both are server-defined for now (4 / 18) and not settable.
- Re-`PUT` keeps the schedule and the last-sent marker (no double send on edit).

Errors: `invalid_id` (400, bad device UUID), `invalid_body` (400: bad JSON, missing `spotIds`/`enabled`, non-UUID id, more than 20 ids, empty `spotIds` while enabled, or no known/active spot left while enabled; unknown/inactive ids alone are skipped, not an error), `plan_limit` (403, Free plan), `not_found` (404, unknown device).

### `GET /v1/devices/{id}/digest`

`200` with the same `data` shape, or `404 not_found` when the device has no digest.

### `DELETE /v1/devices/{id}/digest`

`204` (idempotent).

### Push payload (APNs)

Sent by worker job `digest.weekly` through the same APNs client as alerts (collapse-id `digest-<deviceId>`):

```json
{
  "aps": {"alert": {"title-loc-key": "SPOT_NAMED", "title-loc-args": ["Vondelpark"], "subtitle-loc-key": "DIGEST_WHEN_SATURDAY", "subtitle-loc-args": ["14:00", "17:00"], "loc-key": "DIGEST_BODY", "loc-args": ["88"]}, "sound": "default"},
  "kind": "digest.weekend",
  "spotId": "<uuid>",
  "windowStart": "2026-11-21T13:00:00Z",
  "windowEnd": "2026-11-21T16:00:00Z",
  "score": 88
}
```

- Window search: the coming Saturday and Sunday (device-local dates; on a Sunday only that day), 08:00–20:00 in **each spot's own timezone**, per spot the best contiguous run of 2–4 hours where every hourly score is ≥ 60 (`t_spot_scores`), ranked by mean score (ties: longer, then earlier). The single best (spot, day, window) wins; `score` is the rounded mean. If nothing qualifies, no push is sent.
- Localization keys only ([`PUSH_KEYS.md`](PUSH_KEYS.md)): the day is the key variant (`DIGEST_WHEN_SATURDAY` / `DIGEST_WHEN_SUNDAY`), the window is two 24-hour `HH:MM` args in the **spot's** timezone. Custom keys are top-level next to `aps`. The app routes taps on `kind == "digest.weekend"` + `spotId` to spot detail.
- Idempotent: at most one send (or silent skip) per device per local date. A transient APNs failure releases the slot so the next hourly run retries inside the same local hour.

## Entitlement (Free / Plus)

### `GET /v1/devices/{id}/entitlement`

```json
{"data":{"plan":"plus","productId":"com.pickhour.plus.yearly","expiresAt":"2027-11-11T00:00:00Z","isTrial":true,"autoRenew":true,"ownership":"purchased"}}
```

`plan` is `free` or `plus`; subscription fields are absent on Free. `ownership` is `purchased` or `family_shared` — the latter when Plus comes through Apple Family Sharing (the family member's StoreKit transaction carries `ownershipType: FAMILY_SHARED`; the app links it via `POST /v1/entitlement/link` like any other purchase).

### `POST /v1/entitlement/link`

Attach a StoreKit 2 `signedTransaction` (JWS) to a device:

```json
{"signedTransaction":"<jws>"}
```

→ `{"data":{"plan":"plus"}}`.

The server **verifies** the JWS: `alg` must be `ES256`, the `x5c` chain must lead to the embedded Apple Root CA G3, the signature (raw R||S) must match, `bundleId` must equal the configured `APPSTORE_BUNDLE_ID`, and `environment` must be `Production` (or `Sandbox`, accepted by default because App Review purchases in Sandbox against production; `APPSTORE_ALLOW_SANDBOX=false` rejects it; `Xcode`/`LocalTesting` are always rejected — a StoreKit-testing build cannot link to a production server). The stored `lastSignedDate` is the transaction's own `signedDate`; a stale (older-signed) transaction never overwrites newer state, and a revoked/refunded subscription is terminal: no later transaction re-activates it.

Device binding is **mandatory**: the transaction must carry an `appAccountToken` equal to the `X-Device-ID` UUID (`403 account_mismatch` otherwise) — the app sets it to the device UUID at purchase time. Only products listed in `APPSTORE_PLUS_PRODUCT_IDS` may grant Plus (`invalid_body` otherwise; empty list = unconfigured, accepts all bundle products — set it before go-live). Subscription transactions without `expiresDate` are rejected. A transaction can be linked to at most **5 active devices** (`409 device_limit`; a deleted device frees its slot).

Errors: `invalid_body` (malformed/unsigned/forged JWS, wrong bundle, unsupported environment), `not_found` (unknown device), `account_mismatch` (403), `device_limit` (409), `service_unavailable` (503, verification not configured on the server).

### `POST /v1/appstore/notifications`

App Store Server Notifications V2 webhook: `{"signedPayload":"<jws>"}` → `{"data":{"status":"accepted"}}`. The backend verifies the outer `signedPayload` and the nested `signedTransactionInfo` exactly like `/entitlement/link` (chain, signature, `data.bundleId`, `data.environment`); a notification whose nested transaction fails verification is rejected with `400 invalid_body` (not recorded, not applied). Verified notifications are recorded in `apple_notification` and the embedded transaction is upserted; a notification whose transaction `signedDate` is not newer than the stored one is recorded as `ignored`. `503 service_unavailable` while `APPSTORE_BUNDLE_ID` is unset (Apple retries).

---

## Admin (staff-only)

Two credentials are accepted on `/v1/admin/*`:

- **Session cookie** — the console's real login: `POST /v1/admin/auth/login` `{email,password}` → `{challenge, mode}` (pending challenge, never a session; lifetime `MFA_CHALLENGE_TTL`, default 5m); then `POST /v1/admin/auth/totp` `{challenge, code}` with a 6-digit TOTP (RFC 6238, 30 s step, ±1 skew, replay-rejected per window) or a one-time recovery code `xxxx-xxxx`. First login returns `mode:"enroll"` with `enrollSecret` + `otpauthUrl`; `POST /v1/admin/auth/enroll` confirms one code and returns 10 recovery codes once. Success sets an HttpOnly, Secure, SameSite=Strict `ph_admin_session` cookie (opaque token stored hashed; `MFA_SESSION_TTL` absolute, default 12h, and `MFA_SESSION_IDLE_TTL` idle, default 30m). `POST /v1/admin/auth/logout`, `GET /v1/admin/auth/me`, `GET /v1/admin/auth/sessions`, `POST /v1/admin/auth/sessions/revoke-others`, `POST /v1/admin/auth/recovery-codes` manage it. `POST /v1/admin/auth/password` `{currentPassword,newPassword}` changes the password (minimum 12 characters) and signs out every other session; it answers `400 wrong_password` for a bad current password (not 401, the session stays valid) `400 weak_password` when the new one is under 12 characters and `400 password_unchanged` when it equals the current one. A forgotten password is reset by staff with `admin set-password --email`.
- **Bearer token** — `Authorization: Bearer $MFA_SECRET_KEYS_CLI`, constant-time compared, for scripts/CI only (never typed into the browser).
- **Google account link** (session login only) — `GET /v1/admin/integrations/google/connect` returns `{url}` for the Google consent screen (scopes: `openid email`, `webmasters.readonly`, `analytics.readonly`; `access_type=offline`, HMAC-signed 10-minute state bound to the admin user). Google redirects to `GOOGLE_REDIRECT_URI` → `GET /v1/admin/integrations/google/callback?code=&state=` exchanges the code, seals the refresh token with `MFA_SECRET_KEYS` into `t_admin_google_tokens`, then 302s to `/security?google=connected` (or `?google=error`). `GET /v1/admin/integrations/google` returns `{linked, email, scopes, createdAt, updatedAt}` — never token material. `DELETE /v1/admin/integrations/google` removes the row and best-effort revokes at Google. `GET /v1/admin/integrations/google/resources` (session only) returns `{sites:[{siteUrl,permissionLevel}], properties:[{property,displayName}]}` — the GSC sites and GA4 properties the linked account can read, fetched live with a refreshed access token. `PUT /v1/admin/integrations/google/config` `{gscSiteUrl, ga4PropertyId}` picks which site/property the daily sync pulls (empty string clears). Requires `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`/`GOOGLE_REDIRECT_URI`; `GOOGLE_REDIRECT_URI` must match the console origin so the callback lands on `/security`.

- **Traffic sync** — the `google.sync` worker job (daily, schedule row in `t_worker_job_schedules`) re-pulls the trailing 28 days for every linked, configured account: GSC `searchAnalytics` daily totals + top 500 queries/pages per site into `t_traffic_gsc_daily`/`t_traffic_gsc_dims`, GA4 `runReport` daily sessions/users/pageviews into `t_traffic_ga4_daily`. Reads are precomputed only: `GET /v1/admin/traffic/gsc?days=` → `{days:[{date,clicks,impressions,ctr,position}], queries, pages, queryRange, pageRange}` and `GET /v1/admin/traffic/ga4?days=` → `{days:[{date,sessions,totalUsers,newUsers,pageViews,engagementRate,avgSessionDurationSec}], totals}`, both aggregated across all configured sites/properties.

Accounts are provisioned by the staff CLI (`cmd/admin create-user|reset-2fa|disable-user|enable-user|list`); there is no self sign-up. Passwords are argon2id; TOTP secrets are AES-256-GCM sealed with `MFA_SECRET_KEYS`. Failures are indistinguishable for unknown emails; `MFA_MAX_FAILURES` failed passwords/codes (default 5) lock the account for `MFA_LOCKOUT_TTL` (default 15m), and failed attempts are also rate-limited per IP (`RATE_AUTH_FAIL_PER_MIN`, default 10/min → `429 rate_limited` + `Retry-After`). Mutating cookie-session requests must send the `X-Pickhour-Admin: 1` header and an allowed `Origin` (CSRF control; bearer requests are exempt). When neither `MFA_SECRET_KEYS_CLI` nor `MFA_SECRET_KEYS` is configured the whole surface returns 404. Every mutating admin request writes an audit log line including `admin_user_id` for session requests (request id, method, path, client IP, status — never credentials). Never expose to the public app.

| Endpoint | Purpose |
|---|---|
| `GET /v1/admin/overview` | Headline counts: spots, devices (24h active), pending reports, alert rules/deliveries, Plus devices |
| `GET /v1/admin/freshness` | Newest `fetched_at`/`computed_at`/`updated_at` per pipeline stage — staleness alerting |
| `GET /v1/admin/metrics` | Analytical counters: score distribution + histogram + cap rate (7d), feedback verdicts, report type/status (30d), crowd sources, subscription statuses, alert delivery outcomes |
| `GET /v1/admin/events/breakdown?name=&prop=[&by=][&since=]` | Event props breakdown (see below) |
| `GET /v1/admin/cache` | Response-cache counters per route since this API process started (in-process, per replica): `hit`, `miss`, `coalesced` (waited on a concurrent miss for the same key), `bypass` + `bypassByReason` (`disabled`, `breaker`, `request`, `redis`, `canceled`, `status`, `content`, `size`, `empty`), `storeErrors`, `breakerTrips`, and `hitRate = (hit+coalesced)/(hit+coalesced+miss)` (null until a cacheable request was seen) |
| `GET /v1/admin/jobs?limit=&cursor=` | Recent `job_run` rows, keyset-paginated (`cursor` = last run id from `nextCursor`) |
| `GET /v1/admin/jobs/schedules` | Registered worker job intervals in seconds, including allowed bounds |
| `PATCH /v1/admin/jobs/{job}/schedule` | Update a job interval: `{"intervalSeconds":3600}` |
| `POST /v1/admin/jobs/{job}/run` | Run a job once now. `202` + the schedule with `runRequestedAt` set. The API only records the request (`t_worker_job_schedules.run_requested_at`); a running worker claims it atomically on its next schedule poll (within 30 s) and runs the job under the usual advisory lock, even when the schedule is disabled. With no worker running the request stays pending. `404` for an unknown job |
| `GET /v1/admin/plan-limits` | Every `t_plan_limits` row: `{plan, feature, limit}`; `limit` -1 = unlimited |
| `PUT /v1/admin/plan-limits/{plan}/{feature}` | `{"limit": n}` with n = -1 or 0-1000. Updates an existing row only (`404` otherwise): a limit means nothing until code reads it. Today the only feature is `alert_rules`, read on every alert create, so a change applies immediately |
| `GET /v1/admin/city-packs` | All packs in every status with `activeSpots`, `totalSpots`, `lastImportAt` and `nextStatuses` (the statuses the pack may move to now) |
| `PATCH /v1/admin/city-packs/{id}` | `{"status":"draft"|"beta"|"live"|"retired"}`. Follows the pack state machine (draft→beta|retired, beta→live|draft|retired, live→beta|retired, retired terminal); beta/live need an import. Illegal move → `409 conflict`. Only beta and live packs are served to the app |
| `GET /v1/admin/spots?q=&status=&pack=&limit=` | Staff spot lookup across every status. `q` = name fragment (at least 2 characters, matched literally, case-insensitive) or a spot UUID; `pack` = pack slug; at least one filter is required (`400 invalid_query`). Max 200 rows, default 50, ordered by name |
| `PATCH /v1/admin/spots/{id}` | `{"action":"hide"|"restore"}`. `hide`: pending/active/closed → hidden with `status_reason = hidden_by_admin` (not an importer reason, so imports never bring it back). `restore`: hidden/closed → active. Repeating an action is a no-op `200`; a merged spot → `409 conflict` |
| `GET /v1/admin/reports?limit=&cursor=` | Pending spot_report moderation queue, keyset-paginated (`cursor` = `nextCursor`) |
| `PATCH /v1/admin/reports/{id}` | `{"status":"accepted"|"rejected"}` |
| `GET /v1/admin/api-keys` | All API keys with their grants (admin UI) |
| `POST /v1/admin/api-keys` | Create key `{name, description?, expiresAt?, rateLimitRps?, grants:[{effect,resource,action}]}` → `201` + `secret` shown once |
| `POST /v1/admin/api-keys/{id}/revoke` | Immediately invalidate a key |
| `POST /v1/admin/api-keys/{id}/grants` | Add grant `{effect:"allow"|"deny", resource, action}` |
| `DELETE /v1/admin/api-keys/{id}/grants` | Remove grant (same body) |
| `GET /v1/admin/scoring-config` | All scoring_config versions; `isActive` marks the effective one |
| `POST /v1/admin/scoring-config` | New version `{weights, thresholds, activate}` — draft gets `active_from=9999`, activate=true goes live now |
| `POST /v1/admin/scoring-config/{version}/activate` | Make a stored version effective |

Workers write `job_run` automatically (start → ok/failed/skipped).
Job schedules are stored in the database; running workers reload changes within 30 seconds. Jobs still run once at worker startup. Allowed interval ranges: `weather.refresh` 900–3600s, `crowd.refresh` 3600–2592000s, `crowd.aggregate` 60–86400s, `scoring.recompute` 300–21600s, `alerts.evaluate` 60–3600s, `digest.weekly` 900–3600s (default hourly; must tick at least hourly to hit each local send hour — sends the Plus weekend digest), and `holidays.refresh` 86400–2592000s (default weekly; offline national public holidays for every pack country, current + next year, into `t_holidays`).

### `GET /v1/admin/events/breakdown?name=paywall_feedback&prop=choice[&by=reason][&since=RFC3339]`

Buckets one event name by a prop (`props->>prop`, bound as a parameter) and optionally a second prop. `since` defaults to now − 30 days. `prop`/`by` must match `^[a-zA-Z][a-zA-Z0-9_]{0,31}$`, `name` `^[a-z][a-z0-9_]{0,63}$`, else `400 invalid_query`; bad `since` → `400 invalid_query`. Items are sorted by `count` desc (max 500); `devices` = distinct devices in the bucket (one person may answer several times); events lacking the prop fall into `value: ""`. Missing/wrong admin token as for the other admin routes.

```json
{"data":{"name":"paywall_feedback","prop":"choice","by":"reason","since":"2026-10-14T11:06:39Z",
 "items":[{"value":"shade","by":"forecast","count":2,"devices":1},{"value":"price","by":"general","count":1,"devices":1}]}}
```

Without `by`, items are `{value, count, devices}` and the `by` field is omitted everywhere.

### `POST /v1/events` — product analytics batch

```json
{"events":[{"name":"app_open"},{"name":"paywall_view","props":{"source":"forecast_day3"}}]}
```

→ `202 {"data":{"status":"accepted","count":3}}`. Rules: `name` must match `[a-z][a-z0-9_]{0,63}`; batch 1–100 events; **no location in props**; 5000 events/device/day cap → `rate_limited`; each event's `props` is capped at 4096 bytes serialized (`invalid_body`); the whole request at 512 KiB (`413`). Admin đọc qua `GET /v1/admin/events?days=` (byName/byDay).

## MCP server (agents)

`cmd/mcp` exposes a read-only MCP server over **Streamable HTTP** at `POST /mcp` (default port `MCP_PORT`=8081). Clients authenticate with `Authorization: Bearer phk_...`.

### API keys & scopes

Keys live in `api_key` + `api_key_grant` tables. A grant is `(effect, resource, action)` where each field may be `*`; evaluation is **deny-wins**: any matching deny rejects, otherwise at least one matching allow is required. Secrets are `phk_` + 32 random bytes (base64url); only the SHA-256 hash and a display prefix are stored — the raw secret is shown once at creation.

Manage keys with the staff CLI:

```bash
go run ./cmd/keyctl create --name "ops-agent" --grant spots:read --grant recommendations:read
# prints the secret ONCE
go run ./cmd/keyctl list
keyctl revoke <id>
keyctl grant add <id> spots:read     # allow grant
keyctl grant rm  <id> spots:read
```

Key fields: `expires_at` (optional TTL), `revoked_at`, `last_used_at` (stamped async on each auth), `rate_limit_rps` (stored; enforcement planned).

### Tools (resource:action required)

| Tool | Scope | Description |
|---|---|---|
| `spots_in_bbox` | `spots:read` | Spots + score in a bbox (`west/south/east/north`, optional `when` RFC3339) |
| `spot_get` | `spots:read` | Spot detail: names, amenities, sources |
| `spot_forecast` | `spots:read` | Hourly 0–100 forecast, 7 days |
| `recommend_spots` | `recommendations:read` | Ranked spots near lat/lon |

Example client config (streamable HTTP + bearer):

```json
{
  "mcpServers": {
    "pickhour": {
      "url": "https://mcp.pickhour.app/mcp",
      "headers": { "Authorization": "Bearer phk_..." }
    }
  }
}
```
