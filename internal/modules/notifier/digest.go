package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

// DigestPayload is the APNs body of the weekly weekend digest.
//
//   - kind        — "digest.weekend"
//   - spotId      — UUID string; tap opens SpotDetail
//   - windowStart — RFC3339 UTC start of the best window
//   - windowEnd   — RFC3339 UTC end (exclusive)
//   - score       — rounded mean hourly score of the window
//
// Custom keys are top-level next to "aps" (APNs requirement).
type DigestPayload struct {
	APS         APSBody `json:"aps"`
	Kind        string  `json:"kind"`
	SpotID      string  `json:"spotId"`
	WindowStart string  `json:"windowStart"`
	WindowEnd   string  `json:"windowEnd"`
	Score       int     `json:"score"`
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

func buildDigestPayload(d digest.Delivery, locale string) DigestPayload {
	title, body := digestCopy(locale, d)
	return DigestPayload{
		APS:         APSBody{Alert: AlertContent{Title: title, Body: body}, Sound: "default"},
		Kind:        digest.Kind,
		SpotID:      d.SpotID.String(),
		WindowStart: d.WindowStart.UTC().Format(rfc3339),
		WindowEnd:   d.WindowEnd.UTC().Format(rfc3339),
		Score:       d.Score,
	}
}

var weekdayNames = map[string][7]string{
	"en": {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	"nl": {"Zondag", "Maandag", "Dinsdag", "Woensdag", "Donderdag", "Vrijdag", "Zaterdag"},
}

// digestCopy renders the banner in spot-local time: 12h for en, 24h for nl.
func digestCopy(locale string, d digest.Delivery) (title, body string) {
	loc, err := time.LoadLocation(d.SpotTZ)
	if err != nil {
		loc = time.UTC
	}
	start, end := d.WindowStart.In(loc), d.WindowEnd.In(loc)
	switch baseLang(locale) {
	case "nl":
		return "Je weekendpicknick",
			fmt.Sprintf("%s %s uur bij %s ziet er het best uit (%d).",
				weekdayNames["nl"][start.Weekday()], range24(start, end), d.SpotName, d.Score)
	default:
		return "Your weekend picnic",
			fmt.Sprintf("%s %s at %s looks best (%d).",
				weekdayNames["en"][start.Weekday()], range12(start, end), d.SpotName, d.Score)
	}
}

func clock24(t time.Time) string {
	if t.Minute() == 0 {
		return fmt.Sprintf("%d", t.Hour())
	}
	return fmt.Sprintf("%d:%02d", t.Hour(), t.Minute())
}

func range24(s, e time.Time) string { return clock24(s) + "–" + clock24(e) }

func clock12(t time.Time) (string, string) {
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	m := "AM"
	if t.Hour() >= 12 {
		m = "PM"
	}
	if t.Minute() == 0 {
		return fmt.Sprintf("%d", h), m
	}
	return fmt.Sprintf("%d:%02d", h, t.Minute()), m
}

// range12: "2–5 PM" when both ends share a meridiem, else "11 AM–2 PM".
func range12(s, e time.Time) string {
	sc, sm := clock12(s)
	ec, em := clock12(e)
	if sm == em {
		return sc + "–" + ec + " " + em
	}
	return sc + " " + sm + "–" + ec + " " + em
}

// DigestSender pushes weekly digests via the same APNs client/token provider
// as alert pushes.
type DigestSender struct{ *Sender }

func NewDigestSender(s *Sender) *DigestSender { return &DigestSender{s} }

func (s *DigestSender) Send(ctx context.Context, d digest.Delivery) error {
	token, env, locale, ok, err := s.tokens.DeviceAPNsToken(ctx, d.DeviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}
	body, err := json.Marshal(buildDigestPayload(d, locale))
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
	s.logger.InfoContext(ctx, "digest pushed", "device", d.DeviceID, "status", status)
	return nil
}
