package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
)

type ScoringRepository struct {
	queries *Queries
}

func NewScoringRepository(q *Queries) *ScoringRepository {
	return &ScoringRepository{queries: q}
}

func (r *ScoringRepository) ActiveConfig(ctx context.Context) (scoring.Config, error) {
	row, err := r.queries.GetActiveScoringConfig(ctx)
	if err != nil {
		return scoring.Config{}, err
	}
	return scoring.ParseConfig(row.Version, row.Weights, row.Thresholds)
}

func (r *ScoringRepository) ActiveSpots(ctx context.Context) ([]scoring.SpotForScoring, error) {
	rows, err := r.queries.ListActiveSpotsForScoring(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]scoring.SpotForScoring, 0, len(rows))
	for _, row := range rows {
		out = append(out, scoring.SpotForScoring{
			ID:             row.ID,
			Kind:           row.Kind,
			CellID:         row.CellID,
			TZ:             row.Tz,
			Lat:            row.Lat,
			Lon:            row.Lon,
			LastVerifiedAt: timePtr(row.LastVerifiedAt),
		})
	}
	return out, nil
}

func (r *ScoringRepository) AmenitiesForSpots(ctx context.Context, spotIDs []uuid.UUID) ([]scoring.AmenityRow, error) {
	rows, err := r.queries.ListAmenitiesForSpots(ctx, spotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]scoring.AmenityRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, scoring.AmenityRow{
			SpotID:    row.SpotID,
			Type:      row.Type,
			DistanceM: int(row.DistanceM),
		})
	}
	return out, nil
}

func (r *ScoringRepository) WeatherForCells(ctx context.Context, cellIDs []int64, from, to time.Time) ([]scoring.WeatherRow, error) {
	rows, err := r.queries.ListWeatherForCells(ctx, ListWeatherForCellsParams{
		Column1: cellIDs,
		Ts:      from,
		Ts_2:    to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]scoring.WeatherRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, scoring.WeatherRow{
			CellID:     row.CellID,
			TS:         row.Ts,
			TempC:      float4Ptr(row.Temp),
			FeelsLikeC: float4Ptr(row.FeelsLike),
			PrecipProb: int2Ptr(row.PrecipProb),
			WindKmh:    float4Ptr(row.Wind),
			UV:         float4Ptr(row.Uv),
			Cloud:      int2Ptr(row.Cloud),
		})
	}
	return out, nil
}

func (r *ScoringRepository) CrowdForSpots(ctx context.Context, spotIDs []uuid.UUID) ([]scoring.CrowdRow, error) {
	rows, err := r.queries.ListCrowdEstimatesForSpots(ctx, spotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]scoring.CrowdRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, scoring.CrowdRow{
			SpotID: row.SpotID,
			Dow:    int(row.Dow),
			Hour:   int(row.Hour),
			Level:  int(row.Level),
			Source: row.Source,
		})
	}
	return out, nil
}

func (r *ScoringRepository) UpsertScore(ctx context.Context, w scoring.ScoreWrite) (int64, error) {
	reasons, err := json.Marshal(w.Reasons)
	if err != nil {
		return 0, err
	}
	return r.queries.UpsertSpotScore(ctx, UpsertSpotScoreParams{
		SpotID:        w.SpotID,
		TsHour:        w.TSHour,
		ConfigVersion: w.ConfigVersion,
		Score:         int16(w.Score),
		SWeather:      int2FromPtr(w.SWeather),
		SCrowd:        int2FromPtr(w.SCrowd),
		SAmenity:      int2FromPtr(w.SAmenity),
		SShade:        int2FromPtr(w.SShade),
		SFresh:        int2FromPtr(w.SFresh),
		CapReason:     textFromPtr(w.CapReason),
		Reasons:       reasons,
		Confidence:    int16(w.Confidence),
		ComputedAt:    w.ComputedAt,
	})
}

func float4Ptr(v pgtype.Float4) *float64 {
	if !v.Valid {
		return nil
	}
	f := float64(v.Float32)
	return &f
}

func int2Ptr(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int16)
	return &i
}

func int2FromPtr(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

func textFromPtr(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
