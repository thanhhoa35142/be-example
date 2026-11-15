package crowd

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Spot struct {
	ID         uuid.UUID
	Kind       string
	CityPackID uuid.UUID
	Country    string
	CellID     int64
	Tz         string
	AreaM2     float64
}

type WeatherPoint struct {
	CellID     int64
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
}

type Holiday struct {
	Country string
	Date    time.Time
	Kind    string
}

type ModelEstimate struct {
	SpotID uuid.UUID
	Dow    int
	Hour   int
	Level  int
}

type Repository interface {
	Spots(ctx context.Context) ([]Spot, error)

	Curves(ctx context.Context) (map[CurveKey]float64, error)

	Weather(ctx context.Context, cellIDs []int64, from, to time.Time) ([]WeatherPoint, error)

	Holidays(ctx context.Context, countries []string, from, to time.Time) ([]Holiday, error)

	UpsertModelBatch(ctx context.Context, rows []ModelEstimate) (int64, error)

	CommunityStats(ctx context.Context, minReports int) ([]CommunitySlot, error)

	UpsertCommunity(ctx context.Context, spotID uuid.UUID, dow, hour, level, nReports int) error

	DeleteStaleCommunity(ctx context.Context) (int64, error)
}

type CommunitySlot struct {
	SpotID   uuid.UUID
	Dow      int
	Hour     int
	NReports int
	AvgLevel float64
}
