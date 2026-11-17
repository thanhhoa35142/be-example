package plans

import (
	"math"
	"slices"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

// Config holds the evaluation thresholds (env PLAN_*, see config.Config).
type Config struct {
	// BadBelow: a window mean below this is bad.
	BadBelow int
	// WorseDrop: a mean at least this far below the baseline is worse.
	WorseDrop int
	// BadCaps: any hour capped by one of these (scoring cap_reason) is bad.
	BadCaps []string
	// NotifyHorizon: changes are pushed only this close to the start.
	NotifyHorizon time.Duration
	// Quiet hours in the plan's timezone: [QuietStart, 24) ∪ [0, QuietEnd).
	QuietStart, QuietEnd int
	// ConfirmFrom: plan.confirmed goes out the evening before, from this
	// local hour until QuietStart.
	ConfirmFrom int
	// AltRadiusM: search radius for a nearby alternative spot.
	AltRadiusM int
}

func DefaultConfig() Config {
	return Config{
		BadBelow: 60, WorseDrop: 15, BadCaps: []string{"rain", "heat", "cold"},
		NotifyHorizon: 72 * time.Hour, QuietStart: 22, QuietEnd: 7, ConfirmFrom: 18,
		AltRadiusM: 5000,
	}
}

// HourScore is one scored hour of a spot (t_spot_scores).
type HourScore struct {
	TS        time.Time
	Score     int
	CapReason *string
	Reasons   []spots.Reason
}

// Window summarizes the scored hours of a plan window.
type Window struct {
	Mean  int
	Hours int
	// Cap is the first bad cap (rain/heat/cold) in the window, if any.
	Cap *string
	// Worst is the lowest-scoring hour (first on ties); its reasons explain
	// the window on the share page.
	Worst HourScore
}

// WindowHours lists the score hours overlapping [start, end).
func WindowHours(start, end time.Time) (from, to time.Time) {
	return start.UTC().Truncate(time.Hour), end.UTC()
}

// Summarize builds the window summary; ok is false without any scored hour.
func Summarize(hours []HourScore, cfg Config) (Window, bool) {
	if len(hours) == 0 {
		return Window{}, false
	}
	var w Window
	sum := 0
	for i, h := range hours {
		sum += h.Score
		if i == 0 || h.Score < w.Worst.Score {
			w.Worst = h
		}
		if w.Cap == nil && h.CapReason != nil && slices.Contains(cfg.BadCaps, *h.CapReason) {
			c := *h.CapReason
			w.Cap = &c
		}
	}
	w.Hours = len(hours)
	w.Mean = int(math.Round(float64(sum) / float64(len(hours))))
	return w, true
}

// Classify: bad = a bad cap in the window or mean below BadBelow; worse =
// mean at least WorseDrop below the baseline; otherwise ok.
func Classify(w Window, baseline *int, cfg Config) State {
	if w.Cap != nil || w.Mean < cfg.BadBelow {
		return StateBad
	}
	if baseline != nil && w.Mean <= *baseline-cfg.WorseDrop {
		return StateWorse
	}
	return StateOK
}

// Acceptable: an alternative must itself be ok (no bad cap, mean at least
// BadBelow) and strictly better than the current window.
func Acceptable(alt Window, current Window, cfg Config) bool {
	return alt.Cap == nil && alt.Mean >= cfg.BadBelow && alt.Mean > current.Mean
}
