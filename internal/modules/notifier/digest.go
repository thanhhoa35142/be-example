package notifier

import (
	"context"
	"encoding/json"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

type DigestPayload struct {
	APS         APSBody `json:"aps"`
	Kind        string  `json:"kind"`
	SpotID      string  `json:"spotId"`
	WindowStart string  `json:"windowStart"`
	WindowEnd   string  `json:"windowEnd"`
	Score       int     `json:"score"`
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

// buildDigestPayload: title = the place, subtitle = weekend day + window in
// the spot's timezone, body = the score. The digest only ever covers Saturday
// or Sunday (digest.WeekendDates), hence two subtitle variants.
func buildDigestPayload(d digest.Delivery) DigestPayload {
	loc, err := time.LoadLocation(d.SpotTZ)
	if err != nil {
		loc = time.UTC
	}
	start, end := d.WindowStart.In(loc), d.WindowEnd.In(loc)
	when := KeyDigestWhenSaturday
	if start.Weekday() == time.Sunday {
		when = KeyDigestWhenSunday
	}
	titleKey, titleArgs := spotTitle(d.SpotName, d.SpotKind)
	return DigestPayload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: titleKey, TitleLocArgs: titleArgs,
				SubtitleLocKey: when, SubtitleLocArgs: []string{clock(start), clock(end)},
				LocKey: KeyDigestBody, LocArgs: []string{itoa(d.Score)},
			},
			Sound: "default",
		},
		Kind:        digest.Kind,
		SpotID:      d.SpotID.String(),
		WindowStart: d.WindowStart.UTC().Format(rfc3339),
		WindowEnd:   d.WindowEnd.UTC().Format(rfc3339),
		Score:       d.Score,
	}
}

type DigestSender struct{ *Sender }

func NewDigestSender(s *Sender) *DigestSender { return &DigestSender{s} }

func (s *DigestSender) Send(ctx context.Context, d digest.Delivery) error {
	body, err := json.Marshal(buildDigestPayload(d))
	if err != nil {
		return err
	}
	return s.push(ctx, d.DeviceID, "digest-"+d.DeviceID.String(), digest.Kind, body)
}
