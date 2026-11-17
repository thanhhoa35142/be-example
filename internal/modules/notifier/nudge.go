package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/nudge"
)

const goodDayThread = "goodday"

type NudgePayload struct {
	APS         APSBody `json:"aps"`
	Kind        string  `json:"kind"`
	Slot        string  `json:"slot"`
	SpotID      string  `json:"spotId"`
	Score       int     `json:"score"`
	WindowStart string  `json:"windowStart"`
	WindowEnd   string  `json:"windowEnd"`
}

// buildNudgePayload carries loc keys and args only (spot name and score); the
// app renders the copy. It is a passive notification: no sound, no wake, it
// waits in Notification Center (docs/GOOD_DAY_PUSH_SPEC.md §7).
func buildNudgePayload(d nudge.Delivery) NudgePayload {
	title, body := locGoodDayTitle, locGoodDayTodayBody
	if d.Slot == nudge.SlotWeekend {
		title = locGoodDayWeekendTitle
		body = weekendDayKey(d.WindowStart, d.SpotTZ, locGoodDaySatBody, locGoodDaySunBody)
	}
	relevance := 0.5
	return NudgePayload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: title,
				LocKey:      body,
				LocArgs:     []string{d.SpotName, strconv.Itoa(d.Score)},
			},
			Sound:             "default",
			InterruptionLevel: "passive",
			ThreadID:          goodDayThread,
			RelevanceScore:    &relevance,
		},
		Kind:        nudge.Kind,
		Slot:        d.Slot,
		SpotID:      d.SpotID.String(),
		Score:       d.Score,
		WindowStart: d.WindowStart.UTC().Format(rfc3339),
		WindowEnd:   d.WindowEnd.UTC().Format(rfc3339),
	}
}

type NudgeSender struct{ *Sender }

func NewNudgeSender(s *Sender) *NudgeSender { return &NudgeSender{s} }

// Send pushes one good-day nudge. It does not write the push ledger itself:
// the nudge service records it before marking the delivery sent.
func (s *NudgeSender) Send(ctx context.Context, d nudge.Delivery) error {
	token, env, _, ok, err := s.tokens.DeviceAPNsToken(ctx, d.DeviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}
	body, err := json.Marshal(buildNudgePayload(d))
	if err != nil {
		return err
	}
	if s.client == nil {
		s.logger.InfoContext(ctx, "nudge push (APNs not configured)",
			"device", d.DeviceID, "slot", d.Slot, "score", d.Score)
		return nil
	}
	collapse := "goodday-" + d.LocalDate.Format("2006-01-02")
	status, err := s.client.SendPassive(ctx, token, env, collapse, body)
	if status == http.StatusGone {
		if u, ok := s.tokens.(Unregisterer); ok {
			if uerr := u.MarkDeviceUnregistered(ctx, d.DeviceID); uerr != nil {
				s.logger.ErrorContext(ctx, "device unregister failed", "device", d.DeviceID, "error", uerr)
			}
		}
		return alerts.ErrPushTokenDead
	}
	if err != nil {
		return errors.Join(fmt.Errorf("nudge apns status %d", status), err)
	}
	s.logger.InfoContext(ctx, "nudge pushed", "device", d.DeviceID, "slot", d.Slot, "status", status)
	return nil
}
