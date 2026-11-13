package crowd

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Spot is the subset of spot fields the crowd model needs.
type Spot struct {
	ID         uuid.UUID
	Kind       string
	CityPackID uuid.UUID
	Country    string
	CellID     int64
	Tz         string
	AreaM2     float64
}

// WeatherPoint is one weather_hourly row.
type WeatherPoint struct {
	CellID     int64
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
}

// Holiday is one holiday row; Kind is "public" or "school".
type Holiday struct {
	Country string
	Date    time.Time // date only, UTC
	Kind    string
}

// ModelEstimate is one (spot, dow, hour) model-sourced crowd level.
type ModelEstimate struct {
	SpotID uuid.UUID
	Dow    int
	Hour   int
	Level  int
}

// Repository is the persistence port for the crowd module.
type Repository interface {
	Spots(ctx context.Context) ([]Spot, error)
	// Curves returns hourly factors keyed by (kind, dow, hour).
	Curves(ctx context.Context) (map[CurveKey]float64, error)
	// Weather returns hourly rows for the given cells in [from, to).
	Weather(ctx context.Context, cellIDs []int64, from, to time.Time) ([]WeatherPoint, error)
	// Holidays returns holidays for the given countries in [from, to].
	Holidays(ctx context.Context, countries []string, from, to time.Time) ([]Holiday, error)
	// UpsertModelBatch writes model estimates in one statement. It must not
	// overwrite rows whose source is 'community' or 'provider' (spec: sources
	// never blend silently) and leaves unchanged model rows untouched.
	// Returns the number of rows inserted or changed.
	UpsertModelBatch(ctx context.Context, rows []ModelEstimate) (int64, error)

	// CommunityStats returns aggregated crowd_report rows with at least
	// minReports votes in the last 30 days.
	CommunityStats(ctx context.Context, minReports int) ([]CommunitySlot, error)
	// UpsertCommunity writes one community-sourced estimate.
	UpsertCommunity(ctx context.Context, spotID uuid.UUID, dow, hour, level, nReports int) error
	// DeleteStaleCommunity removes community estimates not refreshed in ~35d
	// so the model can reclaim those slots.
	DeleteStaleCommunity(ctx context.Context) error
}

// CommunitySlot is an aggregated (spot, dow, hour) crowd_report bucket.
type CommunitySlot struct {
	SpotID   uuid.UUID
	Dow      int
	Hour     int
	NReports int
	AvgLevel float64 // mean of 1..3
}
