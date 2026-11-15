package notifier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
)

func testPlanNotification() plans.Notification {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, ams)
	return plans.Notification{
		Kind: plans.KindChanged, PlanID: uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		ShareToken: "AAAAAAAAAAAAAAAAAAAAAA", SpotID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		SpotName: "Vondelpark", SpotKind: "park", Start: start.UTC(), End: start.Add(3 * time.Hour).UTC(),
		TZ: "Europe/Amsterdam", State: plans.StateBad, Score: 41,
	}
}

func TestPlanPayloadKeys(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) // Friday, the day before
	altStart := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	altEnd := altStart.Add(3 * time.Hour)
	spotID := uuid.New()
	cases := []struct {
		name    string
		mut     func(*plans.Notification)
		key     string
		args    []string
		allowed []string
	}{
		{"bad no alt", func(n *plans.Notification) {}, KeyPlanBadNoAlt, []string{"41"}, nil},
		{"bad alt none", func(n *plans.Notification) { n.Alternative = &plans.Alternative{Type: plans.AltNone} }, KeyPlanBadNoAlt, []string{"41"}, nil},
		{"bad alt time", func(n *plans.Notification) {
			n.Alternative = &plans.Alternative{Type: plans.AltTime, Start: &altStart, End: &altEnd, Score: 80}
		}, KeyPlanBadAltTime, []string{"41", "14:00", "17:00", "80"}, nil},
		{"worse alt spot", func(n *plans.Notification) {
			n.State = plans.StateWorse
			n.Alternative = &plans.Alternative{Type: plans.AltSpot, SpotID: &spotID, SpotName: "Oosterpark", SpotKind: "park", Score: 77}
		}, KeyPlanWorseAltSpot, []string{"41", "Oosterpark", "77"}, []string{"Oosterpark"}},
		{"worse alt unnamed", func(n *plans.Notification) {
			n.State = plans.StateWorse
			n.Alternative = &plans.Alternative{Type: plans.AltSpot, SpotID: &spotID, SpotKind: "park", Score: 77}
		}, KeyPlanWorseAltNearby, []string{"41", "77"}, nil},
		{"worse no alt", func(n *plans.Notification) { n.State = plans.StateWorse }, KeyPlanWorseNoAlt, []string{"41"}, nil},
		{"bad alt spot", func(n *plans.Notification) {
			n.Alternative = &plans.Alternative{Type: plans.AltSpot, SpotID: &spotID, SpotName: "Oosterpark", Score: 77}
		}, KeyPlanBadAltSpot, []string{"41", "Oosterpark", "77"}, []string{"Oosterpark"}},
		{"bad alt nearby", func(n *plans.Notification) {
			n.Alternative = &plans.Alternative{Type: plans.AltSpot, SpotID: &spotID, Score: 77}
		}, KeyPlanBadAltNearby, []string{"41", "77"}, nil},
		{"back to ok", func(n *plans.Notification) { n.State = plans.StateOK; n.Score = 82 }, KeyPlanOK, []string{"82"}, nil},
		{"confirmed", func(n *plans.Notification) {
			n.Kind, n.State, n.Score = plans.KindConfirmed, plans.StateOK, 85
		}, KeyPlanConfirmed, []string{"85"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := testPlanNotification()
			c.mut(&n)
			p := buildPlanPayload(n, now)
			if p.APS.Alert.LocKey != c.key || strings.Join(p.APS.Alert.LocArgs, "|") != strings.Join(c.args, "|") {
				t.Fatalf("got %s %v, want %s %v", p.APS.Alert.LocKey, p.APS.Alert.LocArgs, c.key, c.args)
			}
			body, _ := json.Marshal(p)
			assertNoServerText(t, body, c.allowed...)
		})
	}
}

func TestPlanPayloadContract(t *testing.T) {
	now := time.Date(2026, 11, 20, 17, 30, 0, 0, time.UTC)
	p := buildPlanPayload(testPlanNotification(), now)
	a := p.APS.Alert
	if a.TitleLocKey != KeySpotNamed || a.TitleLocArgs[0] != "Vondelpark" {
		t.Fatalf("title %s %v", a.TitleLocKey, a.TitleLocArgs)
	}
	// Subtitle: day relative to the plan tz, window in the plan tz.
	if a.SubtitleLocKey != KeyWhenTomorrow || strings.Join(a.SubtitleLocArgs, "|") != "10:00|13:00" {
		t.Fatalf("subtitle %s %v", a.SubtitleLocKey, a.SubtitleLocArgs)
	}
	if p.APS.ThreadID != "plan-44444444-4444-4444-4444-444444444444" || p.Kind != plans.KindChanged ||
		p.ShareToken == "" || p.Start != "2026-11-21T09:00:00Z" || p.End != "2026-11-21T12:00:00Z" || p.TZ != "Europe/Amsterdam" || p.State != "bad" {
		t.Fatalf("custom fields %+v", p)
	}
	body, _ := json.Marshal(p)
	assertNoServerText(t, body, "Vondelpark")

	n := testPlanNotification()
	n.SpotName = ""
	n.SpotKind = "beach"
	if p := buildPlanPayload(n, now); p.APS.Alert.TitleLocKey != KeySpotBeach || len(p.APS.Alert.TitleLocArgs) != 0 {
		t.Fatalf("unnamed title %+v", p.APS.Alert)
	}
}
