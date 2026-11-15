package notifier

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

func testDigest(tz string, startUTC time.Time, hours int) digest.Delivery {
	return digest.Delivery{
		DeviceID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		SpotID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		SpotName:    "Vondelpark",
		SpotKind:    "park",
		SpotTZ:      tz,
		WindowStart: startUTC,
		WindowEnd:   startUTC.Add(time.Duration(hours) * time.Hour),
		Score:       88,
	}
}

func TestDigestKeys(t *testing.T) {
	sat := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		tz       string
		start    time.Time
		hours    int
		when     string
		from, to string
	}{
		{"Europe/Amsterdam", sat, 3, KeyDigestWhenSaturday, "14:00", "17:00"},
		{"Europe/Amsterdam", sat.Add(-3 * time.Hour), 4, KeyDigestWhenSaturday, "11:00", "15:00"},
		{"Europe/Amsterdam", sat.AddDate(0, 0, 1), 3, KeyDigestWhenSunday, "14:00", "17:00"},
		{"Asia/Kolkata", time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC), 2, KeyDigestWhenSaturday, "14:30", "16:30"},
		// 00:00 UTC Sunday is still Saturday 16:00 in Los Angeles.
		{"America/Los_Angeles", time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), 2, KeyDigestWhenSaturday, "16:00", "18:00"},
		{"Bad/Zone", time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC), 2, KeyDigestWhenSaturday, "14:00", "16:00"},
	}
	for _, c := range cases {
		a := buildDigestPayload(testDigest(c.tz, c.start, c.hours)).APS.Alert
		if a.SubtitleLocKey != c.when || !reflect.DeepEqual(a.SubtitleLocArgs, []string{c.from, c.to}) {
			t.Errorf("%s %s: got %s %v", c.tz, c.start, a.SubtitleLocKey, a.SubtitleLocArgs)
		}
		if a.TitleLocKey != KeySpotNamed || a.LocKey != KeyDigestBody ||
			!reflect.DeepEqual(a.LocArgs, []string{"88"}) {
			t.Errorf("%s: keys %+v", c.tz, a)
		}
	}
}

func TestDigestPayloadContract(t *testing.T) {
	d := testDigest("Europe/Amsterdam", time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC), 3)
	d.SpotName = ""
	b, err := json.Marshal(buildDigestPayload(d))
	if err != nil {
		t.Fatal(err)
	}
	assertNoServerText(t, b)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	want := map[string]any{
		"kind": "digest.weekend", "spotId": "33333333-3333-3333-3333-333333333333",
		"windowStart": "2026-11-21T13:00:00Z", "windowEnd": "2026-11-21T16:00:00Z", "score": float64(88),
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v (%s)", k, m[k], v, b)
		}
	}
	alert := m["aps"].(map[string]any)["alert"].(map[string]any)
	if alert["title-loc-key"] != KeySpotPark {
		t.Fatalf("unnamed spot title: %s", b)
	}
	if _, ok := alert["title-loc-args"]; ok {
		t.Fatalf("unnamed spot must not send title args: %s", b)
	}
}
