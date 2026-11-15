package recommendations

import (
	"context"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type Repository interface {
	Nearby(ctx context.Context, lat, lon, radiusM float64, tsHour time.Time, limit int, f spots.Filter) ([]Item, error)
}
