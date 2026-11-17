package notifier

import (
	"strconv"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

// Push copy is localized on the device: the payload carries APNs
// title-loc-key / loc-key / loc-args and the app resolves them from its
// bundle (docs/GOOD_DAY_PUSH_SPEC.md §3). loc-args are only names and
// numbers, never server-formatted times or dates.

const (
	locAlertTitle = "push.alert.title"
	locAlertBody  = "push.alert.body"

	locDigestTitle   = "push.digest.title"
	locDigestSatBody = "push.digest.sat.body"
	locDigestSunBody = "push.digest.sun.body"

	locGoodDayTitle        = "push.goodday.title"
	locGoodDayTodayBody    = "push.goodday.today.body"
	locGoodDayWeekendTitle = "push.goodday.weekend.title"
	locGoodDaySatBody      = "push.goodday.sat.body"
	locGoodDaySunBody      = "push.goodday.sun.body"
)

const rfc3339 = "2006-01-02T15:04:05Z07:00"

type Payload struct {
	APS         APSBody `json:"aps"`
	Kind        string  `json:"kind"`
	SpotID      string  `json:"spotId"`
	RuleID      string  `json:"ruleId"`
	Score       int     `json:"score"`
	WindowStart string  `json:"windowStart"`
}

type APSBody struct {
	Alert AlertContent `json:"alert"`
	Sound string       `json:"sound"`

	// Optional delivery hints; only the good-day nudge sets them.
	InterruptionLevel string   `json:"interruption-level,omitempty"`
	ThreadID          string   `json:"thread-id,omitempty"`
	RelevanceScore    *float64 `json:"relevance-score,omitempty"`
}

type AlertContent struct {
	TitleLocKey string   `json:"title-loc-key"`
	LocKey      string   `json:"loc-key"`
	LocArgs     []string `json:"loc-args"`
}

func buildPayload(d alerts.Delivery) Payload {
	return Payload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: locAlertTitle,
				LocKey:      locAlertBody,
				LocArgs:     []string{d.SpotName, strconv.Itoa(d.Score)},
			},
			Sound: "default",
		},
		Kind:        "alert.window",
		SpotID:      d.SpotID.String(),
		RuleID:      d.RuleID.String(),
		Score:       d.Score,
		WindowStart: d.WindowStart.UTC().Format(rfc3339),
	}
}

// weekendDayKey picks the Saturday or Sunday variant of a key by the window
// start in the spot's time zone, so no weekday is formatted on the server.
// Anything that is not a Sunday (including an unknown zone falling back to
// UTC) maps to the Saturday key.
func weekendDayKey(start time.Time, tz, satKey, sunKey string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	if start.In(loc).Weekday() == time.Sunday {
		return sunKey
	}
	return satKey
}
