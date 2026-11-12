package weather

import (
	"context"
	"errors"
	"time"
)

// WeatherKit is the production provider per spec (Apple WeatherKit).
// It requires a WeatherKit-enabled App ID and JWT signing credentials;
// until those are configured this stub fails fast so the job is skipped
// rather than silently storing bad data.
type WeatherKit struct {
	ServiceID  string
	KeyID      string
	TeamID     string
	PrivateKey []byte
}

func (w *WeatherKit) Name() string { return "weatherkit" }

func (w *WeatherKit) Forecast(_ context.Context, _, _ float64, _, _ time.Time) ([]Point, error) {
	if w.ServiceID == "" || w.KeyID == "" || w.TeamID == "" || len(w.PrivateKey) == 0 {
		return nil, errors.New("weatherkit: not configured (needs App ID + JWT credentials)")
	}
	return nil, errors.New("weatherkit: client not implemented yet")
}
