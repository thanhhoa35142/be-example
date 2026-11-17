package notifier

import (
	"strconv"
	"strings"
	"time"
)

// Push localization keys. The server sends only these keys and
// language-neutral arguments (place names, integers and 24-hour clock times as
// strings); the app's Localizable strings hold every sentence. The catalog with
// the English reference text of each key is docs/PUSH_KEYS.md — keep both in
// sync.
const (
	// Title: the place. Named spots use the name; unnamed spots their kind.
	KeySpotNamed = "SPOT_NAMED"
	KeySpotPark  = "SPOT_PARK"
	KeySpotBeach = "SPOT_BEACH"
	KeySpotSite  = "SPOT_SITE"
	KeySpotTable = "SPOT_TABLE"

	// alert.window
	KeyAlertWindow = "ALERT_WINDOW"

	// digest.weekend
	KeyDigestWhenSaturday = "DIGEST_WHEN_SATURDAY"
	KeyDigestWhenSunday   = "DIGEST_WHEN_SUNDAY"
	KeyDigestBody         = "DIGEST_BODY"

	// plan.changed / plan.confirmed subtitle: the planned day (relative to
	// the plan's own timezone); args: start and end "HH:MM".
	KeyWhenToday    = "WHEN_TODAY"
	KeyWhenTomorrow = "WHEN_TOMORROW"
	KeyWhenIn2Days  = "WHEN_IN_2_DAYS"
	KeyWhenIn3Days  = "WHEN_IN_3_DAYS"

	// plan.changed body: new state × alternative type.
	KeyPlanWorseAltTime   = "PLAN_WORSE_ALT_TIME"
	KeyPlanWorseAltSpot   = "PLAN_WORSE_ALT_SPOT"
	KeyPlanWorseAltNearby = "PLAN_WORSE_ALT_NEARBY"
	KeyPlanWorseNoAlt     = "PLAN_WORSE_NO_ALT"
	KeyPlanBadAltTime     = "PLAN_BAD_ALT_TIME"
	KeyPlanBadAltSpot     = "PLAN_BAD_ALT_SPOT"
	KeyPlanBadAltNearby   = "PLAN_BAD_ALT_NEARBY"
	KeyPlanBadNoAlt       = "PLAN_BAD_NO_ALT"
	KeyPlanOK             = "PLAN_OK"

	// plan.confirmed body.
	KeyPlanConfirmed = "PLAN_CONFIRMED"
)

// spotTitle picks the title key for a spot: its name when it has one,
// otherwise its kind (never a server-written fallback such as "Park").
func spotTitle(name, kind string) (string, []string) {
	if n := strings.TrimSpace(name); n != "" {
		return KeySpotNamed, []string{n}
	}
	switch kind {
	case "park":
		return KeySpotPark, nil
	case "beach":
		return KeySpotBeach, nil
	case "table":
		return KeySpotTable, nil
	default:
		return KeySpotSite, nil
	}
}

// clock formats a local time as 24-hour "HH:MM". Digits and a colon only:
// no AM/PM markers, which are words and differ by language. Times are always
// in the place's timezone (the spot's or the plan's).
func clock(t time.Time) string {
	return t.Format("15:04")
}

func itoa(v int) string { return strconv.Itoa(v) }

// civil returns the calendar date of t in its own location, at UTC midnight,
// so whole-day differences are exact across DST changes.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// whenKey picks the subtitle key for a start time relative to now, both
// compared as calendar dates in loc. Plans are only notified within 72 h of
// their start, so the difference is 0..3 days; anything beyond clamps to
// WHEN_IN_3_DAYS and a past start (not sent in practice) to WHEN_TODAY.
func whenKey(now, start time.Time, loc *time.Location) string {
	days := int(civil(start.In(loc)).Sub(civil(now.In(loc))).Hours() / 24)
	switch {
	case days <= 0:
		return KeyWhenToday
	case days == 1:
		return KeyWhenTomorrow
	case days == 2:
		return KeyWhenIn2Days
	default:
		return KeyWhenIn3Days
	}
}
