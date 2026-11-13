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
| `requestId` | Server-generated (or echoes your `X-Request-ID` header). Show it in error UI / send to support. |
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
| `invalid_report` | 400 | report type/level/verdict out of domain |
| `not_found` | 404 | spot or device does not exist |
| `merged_spot` | 301 | spot merged — follow `Location` header |
| `conflict` | 409 | e.g. `apnsToken` already bound to another device |
| `rate_limited` | 429 | too many reports — retry later |
| `internal_error` | 500 | unexpected — details only in server logs via `requestId` |

### Request headers

- `X-Request-ID: <uuid>` (optional) — sent for support correlation; echoed in responses.
- `X-Client: pickhour-ios` — identifies the app; sent by the official client.
- `X-Client-Version: <semver>+<build>` — app version, e.g. `0.1.0+3`.
- `X-Client-OS: ios <version>` — OS version string.
- `Content-Type: application/json` on POST/PUT/DELETE.

---

## Endpoints

### `GET /healthz` / `GET /readyz`

Liveness/readiness probes. No auth.

```json
{ "status": "ok" }
```

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
      "confidence": 2
    }
  ]
}
```

| field | notes |
|---|---|
| `components` | per-component 0–100; absent key = missing data (don't render as 0) |
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
| `tz` | no | IANA name — used to derive local dow/hour for crowd reports |
| `prefs` | no | free-form jsonb for user settings |

Response `200`:

```json
{ "data": { "id": "3f6569db-3d46-41c5-a1dd-11335502dcce" } }
```

`409 conflict` if `apnsToken` is already bound to a different device ID.

### `DELETE /v1/devices/{id}`

Soft-delete (GDPR/CCPA unlink). In the same statement it hard-deletes the device's alert rules (their delivery log cascades), weekend digest and analytics events (`t_events`), and clears the stored push token, attestation key and prefs. Crowd reports, spot reports, score feedback and subscription links are kept (anonymous aggregates). `204` on success, `404` if unknown/already deleted.
Until real auth exists, the device UUID acts as the capability token — keep it in Keychain.

---

### `POST /v1/spots/{id}/reports`

Confirm a spot or report it wrong. One report per device/spot/type/**day** — resubmitting same day returns `duplicate` (not an error).

Request:

```json
{ "deviceId": "3f65...", "type": "issue", "issueCode": "no_toilet" }
```

| field | values |
|---|---|
| `type` | `confirm` \| `missing` \| `closed` \| `issue` |
| `issueCode` | optional free-text code, only meaningful with `issue` |

Response `201` (or `200` when duplicate):

```json
{ "data": { "status": "accepted" } }
{ "data": { "status": "duplicate" } }
```

---

### `POST /v1/spots/{id}/crowd-reports`

"Busy / moderate / quiet" vote for the **current** hour slot. Proximity is verified **on-device** — the app must only call this when the user is near the spot; the server never receives coordinates.

Request:

```json
{ "deviceId": "3f65...", "level": 3 }
```

`level`: `1` = quiet, `2` = moderate, `3` = busy.

Response `201`: `{ "data": { "status": "accepted" } }`
`429 rate_limited` after 30 reports/device/24h.

---

### `POST /v1/scores/feedback`

"Is this score right?" verdict on a spot+hour.

```json
{ "deviceId": "3f65...", "spotId": "01a1...", "tsHour": "2026-11-11T14:00:00Z", "verdict": 1 }
```

`verdict`: `-1` (too low) \| `0` (about right) \| `+1` (too high).

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

Alert rules fire a push when the best score in an area meets `minScore` during a local-time window. Free plan: **1 rule**; Plus: unlimited. Windows may cross midnight (`startLocal` > `endLocal`). `daysMask` bit `i` = weekday `i` (0=Sunday … 6=Saturday); e.g. `127` = every day, `65` = Sat+Sun (`64+1`). Each (rule, window) delivers at most once.

### `GET /v1/alerts?deviceId={uuid}`

```json
{"data":{"items":[{"id":"...","deviceId":"...","spotId":"...","h3Cell":null,"radiusM":null,"minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam","enabled":true}]}}
```

### `POST /v1/alerts` — create (201)

```json
{"deviceId":"...","spotId":"...","minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam"}
```

Area: exactly one of `spotId` **or** `h3Cell`+`radiusM` (meters, ≤50 km). Errors: `invalid_body` (bad shape/range), `plan_limit` (403, Free cap), `not_found` (unknown device).

### `PUT /v1/alerts/{id}` / `DELETE /v1/alerts/{id}?deviceId=...`

Same body as create (PUT replaces all fields). DELETE → `204`.

### Push payload (APNs)

When a rule fires, the notifier sends an APNs alert (`apns-push-type: alert`, priority 10, collapse-id = rule ID) to the device's `apnsToken` on `apnsEnv`. Request body:

```json
{
  "aps": {"alert": {"title": "Good window for a picnic", "body": "Vondelpark scores 87 — perfect timing to head out."}, "sound": "default"},
  "kind": "alert.window",
  "spotId": "<uuid>",
  "ruleId": "<uuid>",
  "score": 87,
  "windowStart": "2026-11-20T14:00:00Z"
}
```

- `aps.alert.title`/`body` are localized server-side from `device.locale` (en/nl; fallback en).
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
  "aps": {"alert": {"title": "Your weekend picnic", "body": "Saturday 2–5 PM at Vondelpark looks best (88)."}, "sound": "default"},
  "kind": "digest.weekend",
  "spotId": "<uuid>",
  "windowStart": "2026-11-21T13:00:00Z",
  "windowEnd": "2026-11-21T16:00:00Z",
  "score": 88
}
```

- Window search: the coming Saturday and Sunday (device-local dates; on a Sunday only that day), 08:00–20:00 in **each spot's own timezone**, per spot the best contiguous run of 2–4 hours where every hourly score is ≥ 60 (`t_spot_scores`), ranked by mean score (ties: longer, then earlier). The single best (spot, day, window) wins; `score` is the rounded mean. If nothing qualifies, no push is sent.
- Text is rendered in the spot's local time, 12h for `en` ("Saturday 2–5 PM at …"), 24h for `nl` ("Zaterdag 14–17 uur bij … ziet er het best uit (88)."); locale from `device.locale`, fallback `en`. Custom keys are top-level next to `aps`. The app routes taps on `kind == "digest.weekend"` + `spotId` to spot detail.
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
{"deviceId":"...","signedTransaction":"<jws>"}
```

→ `{"data":{"plan":"plus"}}`. Errors: `invalid_body` (malformed JWS), `not_found` (unknown device).

### `POST /v1/appstore/notifications`

App Store Server Notifications V2 webhook: `{"signedPayload":"<jws>"}` → `{"data":{"status":"accepted"}}`. The backend records every notification in `apple_notification` and upserts the embedded transaction.

---

## Admin (staff-only)

All `/v1/admin/*` endpoints require `Authorization: Bearer $ADMIN_TOKEN`. When `ADMIN_TOKEN` is unset the whole surface returns 404. Never expose to the public app.

| Endpoint | Purpose |
|---|---|
| `GET /v1/admin/overview` | Headline counts: spots, devices (24h active), pending reports, alert rules/deliveries, Plus devices |
| `GET /v1/admin/freshness` | Newest `fetched_at`/`computed_at`/`updated_at` per pipeline stage — staleness alerting |
| `GET /v1/admin/metrics` | Analytical counters: score distribution + histogram + cap rate (7d), feedback verdicts, report type/status (30d), crowd sources, subscription statuses, alert delivery outcomes |
| `GET /v1/admin/events/breakdown?name=&prop=[&by=][&since=]` | Event props breakdown (see below) |
| `GET /v1/admin/jobs?limit=&cursor=` | Recent `job_run` rows, keyset-paginated (`cursor` = last run id from `nextCursor`) |
| `GET /v1/admin/jobs/schedules` | Registered worker job intervals in seconds, including allowed bounds |
| `PATCH /v1/admin/jobs/{job}/schedule` | Update a job interval: `{"intervalSeconds":3600}` |
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
{"deviceId":"...","events":[{"name":"app_open"},{"name":"paywall_view","props":{"source":"forecast_day3"}}]}
```

→ `202 {"data":{"status":"accepted","count":3}}`. Rules: `name` must match `[a-z][a-z0-9_]{0,63}`; batch 1–100 events; **no location in props**; 5000 events/device/day cap → `rate_limited`. Admin đọc qua `GET /v1/admin/events?days=` (byName/byDay).

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
