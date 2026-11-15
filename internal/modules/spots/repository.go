package spots

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	List(ctx context.Context, q ListQuery, tsHour, seriesFrom, seriesTo time.Time, limit int) (items []SpotWithScore, total int, err error)

	WeatherProvider(ctx context.Context, weatherCellID int64) (string, error)
	ListPacks(ctx context.Context) ([]Pack, error)
	GetByID(ctx context.Context, id uuid.UUID) (SpotDetail, error)
	ListAmenities(ctx context.Context, spotID uuid.UUID) ([]Amenity, error)
	ListSources(ctx context.Context, spotID uuid.UUID) ([]Source, error)
	ListForecast(ctx context.Context, spotID uuid.UUID, weatherCellID int64, from, to time.Time) ([]ForecastPoint, error)
}
