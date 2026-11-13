package recommendations

import (
	"context"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

// Repository is the persistence port for the recommendations module.
type Repository interface {
	// Nearby returns active spots within radiusM of (lat, lon), best score
	// first, capped at limit.
	Nearby(ctx context.Context, lat, lon, radiusM float64, tsHour time.Time, limit int, f spots.Filter) ([]Item, error)
}
