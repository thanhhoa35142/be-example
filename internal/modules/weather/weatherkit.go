package weather

import (
	"context"
	"errors"
	"time"
)

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
