package notifier

import (
	"context"
	"encoding/json"
	"strconv"

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

func buildDigestPayload(d digest.Delivery) DigestPayload {
	return DigestPayload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: locDigestTitle,
				LocKey:      weekendDayKey(d.WindowStart, d.SpotTZ, locDigestSatBody, locDigestSunBody),
				LocArgs:     []string{d.SpotName, strconv.Itoa(d.Score)},
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
