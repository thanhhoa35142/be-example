package notifier

import (
	"fmt"

	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

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
}

type AlertContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

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

	b := []byte(locale)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
