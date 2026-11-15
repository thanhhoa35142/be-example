# Push localization keys

Every push the backend sends is built from **APNs localization keys**. The
server writes no sentence in any language: `aps.alert` holds only
`title-loc-key`, `subtitle-loc-key` (where useful) and `loc-key`, plus string
arguments that carry no words. iOS resolves each key from the app's
`Localizable.strings` / String Catalog in the device's current language when
the notification is displayed, so the language follows the phone, not
`device.locale` on the server.

The Go constants live in `internal/modules/notifier/keys.go`; this file is the
contract with the app. Adding, renaming or changing the arguments of a key is a
breaking change: ship the strings in the app first (a key missing from the
app's catalog is displayed literally, e.g. `PLAN_BAD_NO_ALT`).

## Rules for arguments

- `*-loc-args` are always JSON **strings** (APNs requirement).
- Allowed content: integers (`"87"`), 24-hour clock times `"HH:MM"`, and place
  names exactly as stored (`title-loc-args` and the alternative-spot argument
  only). Place names come from OpenStreetMap and are not translated.
- Never: weekday names, "today/tomorrow", month names, AM/PM, localized dates.
  The **day is expressed by the key variant** (`WHEN_TODAY`, `WHEN_TOMORROW`,
  `DIGEST_WHEN_SATURDAY`, …).
- Use positional specifiers (`%1$@`, `%2$@`) in translations whose word order
  differs from English.

### Clock times

Times are sent as 24-hour `"HH:MM"` in the **place's** timezone (the spot's
timezone for alerts and digest, the plan's `tz` for plans), never the phone's.
Rationale: digits and a colon are the same in every language, one payload
serves every recipient of a plan (owner and followers can use different
languages and 12/24-hour settings), and the time a picnic happens is the local
time at the place.

Limits, accepted for now:

- Users whose phone uses a 12-hour clock see `14:00` in the banner, not
  `2 PM`. iOS cannot reformat a loc-arg. A translation may wrap the time but
  cannot convert it.
- A traveller in another timezone sees the place's local time, without a zone
  label.
- Upgrade path (no server change needed): a Notification Service Extension can
  rebuild the text from the custom fields (`windowStart`, `windowEnd`, `start`,
  all RFC 3339 UTC) with `DateFormatter` in the user's locale and 12/24-hour
  preference. Every payload already carries those fields.

## Title keys (all kinds)

The title is the place.

| Key | English | Args |
|---|---|---|
| `SPOT_NAMED` | `%@` | 1: spot name, e.g. `"Vondelpark"` |
| `SPOT_PARK` | `A park near you` | none (unnamed spot of kind `park`) |
| `SPOT_BEACH` | `A beach near you` | none (unnamed `beach`) |
| `SPOT_SITE` | `A picnic spot near you` | none (unnamed `site`, also any unknown kind) |
| `SPOT_TABLE` | `A picnic table near you` | none (unnamed `table`) |

## `alert.window`

Sent by `alerts.evaluate` when a rule's best score reaches `minScore` in its
window. collapse-id = rule id.

| Key | Where | English | Args |
|---|---|---|---|
| `ALERT_WINDOW` | body | `Scores %@ right now — a good time to head out.` | 1: score 0–100, e.g. `"87"` |

```json
{
  "aps": {
    "alert": {
      "title-loc-key": "SPOT_NAMED",
      "title-loc-args": ["Vondelpark"],
      "loc-key": "ALERT_WINDOW",
      "loc-args": ["87"]
    },
    "sound": "default"
  },
  "kind": "alert.window",
  "spotId": "33333333-3333-3333-3333-333333333333",
  "ruleId": "11111111-1111-1111-1111-111111111111",
  "score": 87,
  "windowStart": "2026-11-20T14:00:00Z"
}
```

## `digest.weekend`

Sent by `digest.weekly` (Plus). The digest only covers the coming Saturday or
Sunday, so two day variants suffice. collapse-id = `digest-<deviceId>`.

| Key | Where | English | Args |
|---|---|---|---|
| `DIGEST_WHEN_SATURDAY` | subtitle | `Saturday %1$@–%2$@` | 1: window start `"14:00"`, 2: window end `"17:00"` (spot's timezone) |
| `DIGEST_WHEN_SUNDAY` | subtitle | `Sunday %1$@–%2$@` | same |
| `DIGEST_BODY` | body | `The best picnic window this weekend (%@).` | 1: mean score, e.g. `"88"` |

```json
{
  "aps": {
    "alert": {
      "title-loc-key": "SPOT_NAMED",
      "title-loc-args": ["Vondelpark"],
      "subtitle-loc-key": "DIGEST_WHEN_SATURDAY",
      "subtitle-loc-args": ["14:00", "17:00"],
      "loc-key": "DIGEST_BODY",
      "loc-args": ["88"]
    },
    "sound": "default"
  },
  "kind": "digest.weekend",
  "spotId": "33333333-3333-3333-3333-333333333333",
  "windowStart": "2026-11-21T13:00:00Z",
  "windowEnd": "2026-11-21T16:00:00Z",
  "score": 88
}
```
