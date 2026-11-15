package main

import (
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
)

func plansConfig(cfg config.Config) plans.Config {
	return plans.Config{
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
