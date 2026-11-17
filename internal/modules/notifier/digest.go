package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
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
	token, env, _, ok, err := s.tokens.DeviceAPNsToken(ctx, d.DeviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}
	body, err := json.Marshal(buildDigestPayload(d))
	if err != nil {
		return err
	}
	if s.client == nil {
		s.logger.InfoContext(ctx, "digest push (APNs not configured)",
			"device", d.DeviceID, "spot", d.SpotName, "score", d.Score, "payload", string(body))
		return nil
	}
	status, err := s.client.Send(ctx, token, env, "digest-"+d.DeviceID.String(), body)
	if status == http.StatusGone {
		if u, ok := s.tokens.(Unregisterer); ok {
			if uerr := u.MarkDeviceUnregistered(ctx, d.DeviceID); uerr != nil {
				s.logger.ErrorContext(ctx, "device unregister failed", "device", d.DeviceID, "error", uerr)
			}
		}
		return alerts.ErrPushTokenDead
	}
	if err != nil {
		return errors.Join(fmt.Errorf("digest apns status %d", status), err)
	}
	s.recordSend(ctx, d.DeviceID, digest.Kind)
	s.logger.InfoContext(ctx, "digest pushed", "device", d.DeviceID, "status", status)
	return nil
}
