package notifier

import (
	"fmt"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

// Payload is the APNs HTTP/2 JSON body sent to /3/device/{token}.
//
// Contract with the iOS client (App/NotificationDelegate.swift):
//   - aps.alert.title/body   — banner text, already localized server-side
//   - aps.sound              — "default"
//   - kind                   — "alert.window" (future routing discriminator)
//   - spotId                 — UUID string; tap opens SpotDetail for this spot
//   - ruleId                 — UUID string of the alert_rule that fired
//   - score                  — spot score 0–100 at fire time
//   - windowStart            — RFC3339 UTC start of the alert window
//
// APNs requires custom keys at the top level next to "aps" — do not nest them.
type Payload struct {
	APS         APSBody `json:"aps"`
	Kind        string  `json:"kind"`
	SpotID      string  `json:"spotId"`
	RuleID      string  `json:"ruleId"`
	Score       int     `json:"score"`
	WindowStart string  `json:"windowStart"`
}

type APSBody struct {
	Alert AlertContent `json:"alert"`
	Sound string       `json:"sound"`
	// Badge is omitted: the app does not track a server-side badge count yet.
}

type AlertContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// buildPayload renders the push for the device's locale (BCP-47 prefix match,
// e.g. "nl-NL" → "nl"). Unknown/missing locales fall back to English.
func buildPayload(d alerts.Delivery, locale string) Payload {
	title, body := alertCopy(locale, d)
	return Payload{
		APS: APSBody{
			Alert: AlertContent{Title: title, Body: body},
			Sound: "default",
		},
		Kind:        "alert.window",
		SpotID:      d.SpotID.String(),
		RuleID:      d.RuleID.String(),
		Score:       d.Score,
		WindowStart: d.WindowStart.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// alertCopy is the server-side banner catalog. Keep in sync with the app's
// supported locales; the app itself never sees these strings (they are baked
// into the push), so add a language here when the app ships it.
func alertCopy(locale string, d alerts.Delivery) (title, body string) {
	switch baseLang(locale) {
	case "nl":
		return "Goed moment voor een picknick",
			fmt.Sprintf("%s scoort %d — perfect moment om op pad te gaan.", d.SpotName, d.Score)
	default:
		return "Good window for a picnic",
			fmt.Sprintf("%s scores %d — perfect timing to head out.", d.SpotName, d.Score)
	}
}

func baseLang(locale string) string {
	for i, r := range locale {
		if r == '-' || r == '_' {
			locale = locale[:i]
			break
		}
	}
	if locale == "" {
		return "en"
	}
	// ASCII fold: "NL" → "nl"; non-ASCII stays as-is and misses the catalog.
	b := []byte(locale)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
