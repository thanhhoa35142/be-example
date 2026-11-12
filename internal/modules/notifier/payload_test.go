package notifier

import (
	"encoding/json"
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
		Score:       87,
	}
}

func TestBuildPayloadContract(t *testing.T) {
	body, err := json.Marshal(buildPayload(testDelivery(), "en"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	// Custom keys must be top-level next to "aps" (APNs requirement).
	for _, key := range []string{"spotId", "ruleId", "score", "windowStart", "kind"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("payload missing top-level key %q: %s", key, body)
		}
	}
	if m["spotId"] != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("spotId mismatch: %v", m["spotId"])
	}
	if m["score"].(float64) != 87 {
		t.Fatalf("score mismatch: %v", m["score"])
	}

	aps, ok := m["aps"].(map[string]any)
	if !ok {
		t.Fatalf("aps missing or wrong type: %s", body)
	}
	alert, ok := aps["alert"].(map[string]any)
	if !ok || alert["title"] == "" || alert["body"] == "" {
		t.Fatalf("aps.alert.title/body missing: %s", body)
	}
	if aps["sound"] != "default" {
		t.Fatalf("aps.sound: %v", aps["sound"])
	}
}

func TestAlertCopyLocales(t *testing.T) {
	cases := map[string]string{
		"en":      "en",
		"en-US":   "en",
		"nl":      "nl",
		"nl-NL":   "nl",
		"NL_be":   "nl",
		"de":      "en", // fallback
		"":        "en",
		"zh-Hans": "en",
	}
	for in, wantTitle := range cases {
		title, body := alertCopy(in, testDelivery())
		if title == "" || body == "" {
			t.Fatalf("locale %q produced empty copy", in)
		}
		isNL := baseLang(in) == "nl"
		if (wantTitle == "nl") != isNL {
			t.Fatalf("locale %q: baseLang=%q want %q", in, baseLang(in), wantTitle)
		}
	}
	// Dutch copy sanity.
	title, _ := alertCopy("nl", testDelivery())
	if title != "Goed moment voor een picknick" {
		t.Fatalf("nl title: %q", title)
	}
}
