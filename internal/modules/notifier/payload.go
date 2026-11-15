package notifier

import (
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
	// ThreadID groups notifications in Notification Center (one per plan).
	ThreadID string `json:"thread-id,omitempty"`
}

// AlertContent is an APNs alert made only of localization keys and
// language-neutral arguments; the app resolves every key from its own
// Localizable strings (docs/PUSH_KEYS.md). The server writes no sentence.
type AlertContent struct {
	TitleLocKey     string   `json:"title-loc-key"`
	TitleLocArgs    []string `json:"title-loc-args,omitempty"`
	SubtitleLocKey  string   `json:"subtitle-loc-key,omitempty"`
	SubtitleLocArgs []string `json:"subtitle-loc-args,omitempty"`
	LocKey          string   `json:"loc-key"`
	LocArgs         []string `json:"loc-args,omitempty"`
}

const kindAlertWindow = "alert.window"

func buildPayload(d alerts.Delivery) Payload {
	titleKey, titleArgs := spotTitle(d.SpotName, d.SpotKind)
	return Payload{
		APS: APSBody{
			Alert: AlertContent{
				TitleLocKey: titleKey, TitleLocArgs: titleArgs,
				LocKey: KeyAlertWindow, LocArgs: []string{itoa(d.Score)},
			},
			Sound: "default",
		},
		Kind:        kindAlertWindow,
		SpotID:      d.SpotID.String(),
		RuleID:      d.RuleID.String(),
		Score:       d.Score,
		WindowStart: d.WindowStart.UTC().Format(rfc3339),
	}
}
