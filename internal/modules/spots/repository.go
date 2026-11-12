package spots

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the port the spots module depends on.
// Implementations live in infrastructure/persistence.
type Repository interface {
	ListInBBox(ctx context.Context, bbox BBox, tsHour time.Time) ([]SpotWithScore, error)
	GetByID(ctx context.Context, id uuid.UUID) (SpotDetail, error)
	ListAmenities(ctx context.Context, spotID uuid.UUID) ([]Amenity, error)
	ListSources(ctx context.Context, spotID uuid.UUID) ([]Source, error)
	ListForecast(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]ForecastPoint, error)
}
