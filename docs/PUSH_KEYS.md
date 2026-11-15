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

## `plan.changed` / `plan.confirmed`

Sent by `plans.evaluate` to the plan's owner and followers (rules in
API.md, "Plan pushes"). The title is the plan's spot (title keys above).
collapse-id and `thread-id` = `plan-<planId>`: one Notification Center thread
per plan, and a newer plan push replaces an undelivered older one. All times
are in the plan's `tz`.

### Subtitle: the planned day

Picked by comparing the plan's start date with today, both as calendar dates
in the plan's `tz` (a push only goes out within 72 h of the start, so 0–3
days).

| Key | English | Args |
|---|---|---|
| `WHEN_TODAY` | `Today %1$@–%2$@` | 1: plan start `"10:00"`, 2: plan end `"13:00"` |
| `WHEN_TOMORROW` | `Tomorrow %1$@–%2$@` | same |
| `WHEN_IN_2_DAYS` | `In 2 days, %1$@–%2$@` | same |
| `WHEN_IN_3_DAYS` | `In 3 days, %1$@–%2$@` | same |

### Body: `plan.changed`

`worse` = the score fell at least 15 below what it was when the plan was
made; `bad` = rain/heat/cold expected, or the score below 60. Arg 1 is always
the plan window's score now.

| Key | English | Args |
|---|---|---|
| `PLAN_WORSE_ALT_TIME` | `Now %1$@. %2$@–%3$@ looks better (%4$@).` | 1: score, 2: alt start `"14:00"`, 3: alt end `"17:00"`, 4: alt score |
| `PLAN_WORSE_ALT_SPOT` | `Now %1$@. %2$@ nearby looks better (%3$@).` | 1: score, 2: alt spot name `"Oosterpark"`, 3: alt score |
| `PLAN_WORSE_ALT_NEARBY` | `Now %1$@. A spot nearby looks better (%2$@).` | 1: score, 2: alt score (unnamed alternative spot) |
| `PLAN_WORSE_NO_ALT` | `The forecast got worse (%@).` | 1: score |
| `PLAN_BAD_ALT_TIME` | `Not looking good (%1$@). Try %2$@–%3$@ instead (%4$@).` | as `PLAN_WORSE_ALT_TIME` |
| `PLAN_BAD_ALT_SPOT` | `Not looking good (%1$@). Try %2$@ instead (%3$@).` | as `PLAN_WORSE_ALT_SPOT` |
| `PLAN_BAD_ALT_NEARBY` | `Not looking good (%1$@). A spot nearby looks better (%2$@).` | as `PLAN_WORSE_ALT_NEARBY` |
| `PLAN_BAD_NO_ALT` | `Not looking good (%@). No better time or spot nearby.` | 1: score |
| `PLAN_OK` | `Good news: back on track (%@).` | 1: score (state returned to `ok`) |

The alternative's time is the same local day as the plan. `capReason`
(`rain` / `heat` / `cold`) is in the custom fields; the app may pick a more
specific wording from it when it renders the plan.

### Body: `plan.confirmed`

The evening before (18:00–22:00 in the plan's `tz`), once, when the plan
stayed `ok`.

| Key | English | Args |
|---|---|---|
| `PLAN_CONFIRMED` | `Still looking good (%@). Enjoy your picnic!` | 1: score |

```json
{
  "aps": {
    "alert": {
      "title-loc-key": "SPOT_NAMED",
      "title-loc-args": ["Vondelpark"],
      "subtitle-loc-key": "WHEN_TOMORROW",
      "subtitle-loc-args": ["10:00", "13:00"],
      "loc-key": "PLAN_BAD_ALT_TIME",
      "loc-args": ["41", "14:00", "17:00", "80"]
    },
    "sound": "default",
    "thread-id": "plan-44444444-4444-4444-4444-444444444444"
  },
  "kind": "plan.changed",
  "planId": "44444444-4444-4444-4444-444444444444",
  "shareToken": "q3Zr7l0bVQpX2b9kz8w1Hg",
  "spotId": "33333333-3333-3333-3333-333333333333",
  "start": "2026-11-21T09:00:00Z",
  "end": "2026-11-21T12:00:00Z",
  "tz": "Europe/Amsterdam",
  "state": "bad",
  "score": 41,
  "capReason": "rain",
  "alternative": {
    "type": "time",
    "start": "2026-11-21T13:00:00Z",
    "end": "2026-11-21T16:00:00Z",
    "score": 80
  }
}
```

Opening: the owner opens by `planId`; a follower by `shareToken` (same
target as `pickhour://plan/<shareToken>`). `alternative` has the shape of
the plan object's `alternative` (`type` `time` | `spot` | `none`).
