package scoring

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type SpotForScoring struct {
	ID             uuid.UUID
	Kind           string
	CellID         int64
	TZ             string
	Lat, Lon       float64
	LastVerifiedAt *time.Time
}

type AmenityRow struct {
	SpotID    uuid.UUID
	Type      string
	DistanceM int
}

type WeatherRow struct {
	CellID     int64
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
	WindKmh    *float64
	UV         *float64
	Cloud      *int
}

type CrowdRow struct {
	SpotID uuid.UUID
	Dow    int
	Hour   int
	Level  int
	Source string
}

type ScoreWrite struct {
	SpotID        uuid.UUID
	TSHour        time.Time
	ConfigVersion int16
	Score         int
	SWeather      *int
	SCrowd        *int
	SAmenity      *int
	SShade        *int
	SFresh        *int
	CapReason     *string
	Reasons       []Reason
	Confidence    int
}

type Repository interface {
	ActiveConfig(ctx context.Context) (Config, error)
	ActiveSpots(ctx context.Context) ([]SpotForScoring, error)
	AmenitiesForSpots(ctx context.Context, spotIDs []uuid.UUID) ([]AmenityRow, error)
	WeatherForCells(ctx context.Context, cellIDs []int64, from, to time.Time) ([]WeatherRow, error)
	CrowdForSpots(ctx context.Context, spotIDs []uuid.UUID) ([]CrowdRow, error)

	UpsertScores(ctx context.Context, configVersion int16, rows []ScoreWrite, computedAt time.Time) (int64, error)
}
