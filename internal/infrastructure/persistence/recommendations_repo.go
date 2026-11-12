package persistence

import (
	"context"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type RecommendationsRepo struct {
	q *Queries
}

func NewRecommendationsRepo(q *Queries) *RecommendationsRepo {
	return &RecommendationsRepo{q: q}
}

func (r *RecommendationsRepo) Nearby(ctx context.Context, lat, lon, radiusM float64, tsHour time.Time, limit int) ([]recommendations.Item, error) {
	rows, err := r.q.ListRecommendedSpots(ctx, ListRecommendedSpotsParams{
		StMakepoint:   lat,
		StMakepoint_2: lon,
		TsHour:        tsHour,
		StDwithin:     radiusM,
		Limit:         int32(limit),
	})
	if err != nil {
		return nil, err
	}

	out := make([]recommendations.Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, recommendations.Item{
			Spot: spots.Spot{
				ID:             row.ID,
				Kind:           spots.Kind(row.Kind),
				Name:           textPtr(row.Name),
				Names:          namesFromJSON(row.Names),
				Lon:            row.Lon,
				Lat:            row.Lat,
				Country:        row.Country,
				City:           textPtr(row.City),
				TZ:             row.Tz,
				LastVerifiedAt: timePtr(row.LastVerifiedAt),
			},
			DistanceM: row.DistanceM,
			Score:     scoreSnapshotFromRow(row.Score, row.Confidence, row.CapReason, row.Reasons, row.ComputedAt),
		})
	}
	return out, nil
}
