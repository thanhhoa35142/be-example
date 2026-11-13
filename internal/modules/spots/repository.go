package spots

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the port the spots module depends on.
// Implementations live in infrastructure/persistence.
type Repository interface {
	// List returns active spots for the query; tsHour is the hour for Score and
	// [seriesFrom, seriesTo) bounds the hourly series (empty range = none).
	List(ctx context.Context, q ListQuery, tsHour, seriesFrom, seriesTo time.Time) ([]SpotWithScore, error)
	// WeatherProvider returns the provider name behind the cell's forecast
	// rows, or "" when the cell has none.
	WeatherProvider(ctx context.Context, weatherCellID int64) (string, error)
	ListPacks(ctx context.Context) ([]Pack, error)
	GetByID(ctx context.Context, id uuid.UUID) (SpotDetail, error)
	ListAmenities(ctx context.Context, spotID uuid.UUID) ([]Amenity, error)
	ListSources(ctx context.Context, spotID uuid.UUID) ([]Source, error)
	ListForecast(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]ForecastPoint, error)
}
