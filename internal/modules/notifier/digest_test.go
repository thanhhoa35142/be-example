package notifier

import (
	"encoding/json"
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
		SpotTZ:      tz,
		WindowStart: startUTC,
		WindowEnd:   startUTC.Add(time.Duration(hours) * time.Hour),
		Score:       88,
	}
}

func TestDigestCopy(t *testing.T) {
	// Sat 2026-11-21 14:00 Amsterdam (CET, UTC+1) = 13:00Z.
	sat := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		locale, tz  string
		start       time.Time
		hours       int
		title, body string
	}{
		{"en", "Europe/Amsterdam", sat, 3, "Your weekend picnic", "Saturday 2–5 PM at Vondelpark looks best (88)."},
		{"en-GB", "Europe/Amsterdam", sat.Add(-3 * time.Hour), 4, "Your weekend picnic", "Saturday 11 AM–3 PM at Vondelpark looks best (88)."},
		{"", "Europe/Amsterdam", sat.Add(-1 * time.Hour), 2, "Your weekend picnic", "Saturday 1–3 PM at Vondelpark looks best (88)."},
		{"en", "Europe/Amsterdam", sat.Add(-1*time.Hour).AddDate(0, 0, 1), 2, "Your weekend picnic", "Sunday 1–3 PM at Vondelpark looks best (88)."},
		{"nl-NL", "Europe/Amsterdam", sat, 3, "Je weekendpicknick", "Zaterdag 14–17 uur bij Vondelpark ziet er het best uit (88)."},
		{"NL", "Europe/Amsterdam", sat.AddDate(0, 0, 1), 3, "Je weekendpicknick", "Zondag 14–17 uur bij Vondelpark ziet er het best uit (88)."},
		{"en", "Asia/Kolkata", time.Date(2026, 11, 21, 8, 30, 0, 0, time.UTC), 2, "Your weekend picnic", "Saturday 2–4 PM at Vondelpark looks best (88)."},
		{"en", "Asia/Kolkata", time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC), 2, "Your weekend picnic", "Saturday 2:30–4:30 PM at Vondelpark looks best (88)."},
		// Local date differs from UTC date: Sun 00:00Z is still Sat 16:00 in LA.
		{"en", "America/Los_Angeles", time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), 2, "Your weekend picnic", "Saturday 4–6 PM at Vondelpark looks best (88)."},
		{"en", "Europe/Amsterdam", time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), 2, "Your weekend picnic", "Sunday 11 AM–1 PM at Vondelpark looks best (88)."},
		{"en", "Europe/Amsterdam", time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC), 1, "Your weekend picnic", "Sunday 12–1 PM at Vondelpark looks best (88)."},
		{"fr", "Bad/Zone", time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC), 2, "Your weekend picnic", "Saturday 2–4 PM at Vondelpark looks best (88)."},
	}
	for _, c := range cases {
		title, body := digestCopy(c.locale, testDigest(c.tz, c.start, c.hours))
		if title != c.title || body != c.body {
			t.Errorf("%s %s: got %q / %q, want %q / %q", c.locale, c.tz, title, body, c.title, c.body)
		}
	}
}

func TestDigestPayloadContract(t *testing.T) {
	d := testDigest("Europe/Amsterdam", time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC), 3)
	b, err := json.Marshal(buildDigestPayload(d, "en"))
	if err != nil {
		t.Fatal(err)
	}
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
	if _, ok := m["aps"].(map[string]any)["alert"]; !ok {
		t.Fatalf("missing aps.alert: %s", b)
	}
}
