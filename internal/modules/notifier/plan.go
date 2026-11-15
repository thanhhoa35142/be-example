package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
)

// PlanPayload is the APNs body of plan.changed / plan.confirmed. Custom
// fields let the app (or a Notification Service Extension) open the plan and
// re-render the text without the server.
type PlanPayload struct {
	APS         APSBody            `json:"aps"`
	Kind        string             `json:"kind"`
	PlanID      string             `json:"planId"`
	ShareToken  string             `json:"shareToken"`
	SpotID      string             `json:"spotId"`
	Start       string             `json:"start"`
	End         string             `json:"end"`
	TZ          string             `json:"tz"`
	State       string             `json:"state"`
	Score       int                `json:"score"`
	CapReason   *string            `json:"capReason,omitempty"`
	Alternative *plans.Alternative `json:"alternative,omitempty"`
}

// planBodyKey picks the body key and its arguments; times are in loc.
func planBodyKey(n plans.Notification, loc *time.Location) (string, []string) {
	score := itoa(n.Score)
	if n.Kind == plans.KindConfirmed {
		return KeyPlanConfirmed, []string{score}
	}
	if n.State == plans.StateOK {
		return KeyPlanOK, []string{score}
	}
	bad := n.State == plans.StateBad
	pick := func(badKey, worseKey string) string {
		if bad {
			return badKey
		}
		return worseKey
	}
	a := n.Alternative
	switch {
	case a != nil && a.Type == plans.AltTime && a.Start != nil && a.End != nil:
		return pick(KeyPlanBadAltTime, KeyPlanWorseAltTime),
			[]string{score, clock(a.Start.In(loc)), clock(a.End.In(loc)), itoa(a.Score)}
	case a != nil && a.Type == plans.AltSpot && strings.TrimSpace(a.SpotName) != "":
		return pick(KeyPlanBadAltSpot, KeyPlanWorseAltSpot),
			[]string{score, strings.TrimSpace(a.SpotName), itoa(a.Score)}
	case a != nil && a.Type == plans.AltSpot:
		return pick(KeyPlanBadAltNearby, KeyPlanWorseAltNearby), []string{score, itoa(a.Score)}
	default:
		return pick(KeyPlanBadNoAlt, KeyPlanWorseNoAlt), []string{score}
	}
}

func buildPlanPayload(n plans.Notification, now time.Time) PlanPayload {
	loc, err := time.LoadLocation(n.TZ)
	if err != nil {
		loc = time.UTC
	}
	titleKey, titleArgs := spotTitle(n.SpotName, n.SpotKind)
	bodyKey, bodyArgs := planBodyKey(n, loc)
	return PlanPayload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: titleKey, TitleLocArgs: titleArgs,
				SubtitleLocKey:  whenKey(now, n.Start, loc),
				SubtitleLocArgs: []string{clock(n.Start.In(loc)), clock(n.End.In(loc))},
				LocKey:          bodyKey, LocArgs: bodyArgs,
			},
			Sound:    "default",
			ThreadID: "plan-" + n.PlanID.String(),
		},
		Kind:        n.Kind,
		PlanID:      n.PlanID.String(),
		ShareToken:  n.ShareToken,
		SpotID:      n.SpotID.String(),
		Start:       n.Start.UTC().Format(rfc3339),
		End:         n.End.UTC().Format(rfc3339),
		TZ:          n.TZ,
		State:       string(n.State),
		Score:       n.Score,
		CapReason:   n.CapReason,
		Alternative: n.Alternative,
	}
}

// PlanSender adapts Sender to plans.Sender. collapse-id and thread-id are
// "plan-<id>": a newer plan push replaces the older one on the device.
type PlanSender struct {
	s   *Sender
	now func() time.Time
}

func NewPlanSender(s *Sender) *PlanSender { return &PlanSender{s: s, now: time.Now} }

func (p *PlanSender) SendPlan(ctx context.Context, deviceID uuid.UUID, n plans.Notification) error {
	body, err := json.Marshal(buildPlanPayload(n, p.now()))
	if err != nil {
		return err
	}
	err = p.s.push(ctx, deviceID, "plan-"+n.PlanID.String(), n.Kind, body)
	switch {
	case errors.Is(err, alerts.ErrNoPushToken):
		return plans.ErrNoPushToken
	case errors.Is(err, alerts.ErrPushTokenDead):
		return plans.ErrPushTokenDead
	}
	return err
}
