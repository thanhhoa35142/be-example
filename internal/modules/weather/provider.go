package weather

import (
	"context"
	"time"
)

type Provider interface {
	Name() string
	Forecast(ctx context.Context, lat, lng float64, from, to time.Time) ([]Point, error)
}
