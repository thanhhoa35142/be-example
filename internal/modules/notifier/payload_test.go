package notifier

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

func testDelivery() alerts.Delivery {
	return alerts.Delivery{
		RuleID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DeviceID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		WindowStart: time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC),
		SpotID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		SpotName:    "Vondelpark",
		SpotKind:    "park",
		Score:       87,
	}
}

// neutralArg: body/subtitle loc-args may only be integers or 24-hour clock
// times. No words, weekdays or AM/PM. (Place names go in title-loc-args.)
var neutralArg = regexp.MustCompile(`^([0-9]+|[0-2][0-9]:[0-5][0-9])$`)

func assertNoServerText(t *testing.T, body []byte, allowed ...string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	alert := m["aps"].(map[string]any)["alert"].(map[string]any)
	for _, k := range []string{"title", "subtitle", "body"} {
		if _, ok := alert[k]; ok {
			t.Fatalf("aps.alert.%s present; server must send keys only: %s", k, body)
		}
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, k := range []string{"loc-args", "subtitle-loc-args"} {
		args, _ := alert[k].([]any)
		for _, a := range args {
			s := a.(string)
			if !neutralArg.MatchString(s) && !ok[s] {
				t.Fatalf("%s %q is not language-neutral: %s", k, s, body)
			}
		}
	}
}

func TestBuildPayloadContract(t *testing.T) {
	body, err := json.Marshal(buildPayload(testDelivery()))
	if err != nil {
		t.Fatal(err)
	}
	assertNoServerText(t, body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	want := map[string]any{
		"kind": "alert.window", "spotId": "33333333-3333-3333-3333-333333333333",
		"ruleId": "11111111-1111-1111-1111-111111111111", "score": float64(87),
		"windowStart": "2026-11-20T14:00:00Z",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	aps := m["aps"].(map[string]any)
	if aps["sound"] != "default" {
		t.Fatalf("aps.sound: %v", aps["sound"])
	}
	alert := aps["alert"].(map[string]any)
	if alert["title-loc-key"] != KeySpotNamed || alert["loc-key"] != KeyAlertWindow {
		t.Fatalf("keys: %v", alert)
	}
	if !reflect.DeepEqual(alert["title-loc-args"], []any{"Vondelpark"}) ||
		!reflect.DeepEqual(alert["loc-args"], []any{"87"}) {
		t.Fatalf("args: %v", alert)
	}
}

func TestSpotTitle(t *testing.T) {
	cases := []struct {
		name, kind, key string
		args            []string
	}{
		{"Vondelpark", "park", KeySpotNamed, []string{"Vondelpark"}},
		{"  ", "park", KeySpotPark, nil},
		{"", "beach", KeySpotBeach, nil},
		{"", "table", KeySpotTable, nil},
		{"", "site", KeySpotSite, nil},
		{"", "", KeySpotSite, nil},
	}
	for _, c := range cases {
		key, args := spotTitle(c.name, c.kind)
		if key != c.key || !reflect.DeepEqual(args, c.args) {
			t.Errorf("%q/%q: got %s %v", c.name, c.kind, key, args)
		}
	}
}

func TestWhenKey(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 11, 20, 22, 30, 0, 0, time.UTC) // 23:30 Fri in Amsterdam
	cases := []struct {
		start time.Time
		want  string
	}{
		{time.Date(2026, 11, 20, 22, 45, 0, 0, time.UTC), KeyWhenToday},
		{time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC), KeyWhenTomorrow}, // 00:00 Sat local
		{time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC), KeyWhenIn2Days},
		{time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC), KeyWhenIn3Days},
		{time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC), KeyWhenIn3Days},
		{time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC), KeyWhenToday},
	}
	for _, c := range cases {
		if got := whenKey(now, c.start, ams); got != c.want {
			t.Errorf("start %s: got %s want %s", c.start, got, c.want)
		}
	}
	// DST: Amsterdam leaves summer time on 2026-10-25 (a 25-hour day).
	now = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	if got := whenKey(now, time.Date(2026, 10, 25, 22, 30, 0, 0, time.UTC), ams); got != KeyWhenTomorrow {
		t.Errorf("DST: got %s", got)
	}
}
