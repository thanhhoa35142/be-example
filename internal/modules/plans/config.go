package plans

import (
	"time"

	"github.com/thanhhoa35142/be-example/internal/platform/config"
)

// ConfigFrom maps the PLAN_* settings (API and worker share them).
func ConfigFrom(cfg config.Config) Config {
	return Config{
		BadBelow:      cfg.PlanBadBelow,
		WorseDrop:     cfg.PlanWorseDrop,
		BadCaps:       cfg.PlanBadCaps,
		NotifyHorizon: time.Duration(cfg.PlanNotifyHorizonH) * time.Hour,
		QuietStart:    cfg.PlanQuietStart,
		QuietEnd:      cfg.PlanQuietEnd,
		ConfirmFrom:   cfg.PlanConfirmFrom,
		AltRadiusM:    cfg.PlanAltRadiusM,
	}
}
