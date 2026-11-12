package weather

import (
	"context"
	"time"
)

// Provider fetches hourly forecasts for a lat/lng point.
// Implementations: Open-Meteo (dev, no key), WeatherKit (production, Apple creds).
type Provider interface {
	Name() string
	Forecast(ctx context.Context, lat, lng float64, from, to time.Time) ([]Point, error)
}
