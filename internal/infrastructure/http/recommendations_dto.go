package http

import (
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type recommendationItemDTO struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Name           *string        `json:"name"`
	Names          spots.Names    `json:"names"`
	Coordinates    [2]float64     `json:"coordinates"`
	Country        string         `json:"country"`
	City           *string        `json:"city"`
	TZ             string         `json:"tz"`
	LastVerifiedAt *time.Time     `json:"lastVerifiedAt"`
	DistanceM      float64        `json:"distanceM"`
	Score          map[string]any `json:"score"`
}

func toRecommendationItems(items []recommendations.Item) []recommendationItemDTO {
	out := make([]recommendationItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, recommendationItemDTO{
			ID:             item.ID.String(),
			Kind:           string(item.Kind),
			Name:           item.Name,
			Names:          item.Names,
			Coordinates:    [2]float64{item.Lon, item.Lat},
			Country:        item.Country,
			City:           item.City,
			TZ:             item.TZ,
			LastVerifiedAt: utcPtr(item.LastVerifiedAt),
			DistanceM:      item.DistanceM,
			Score:          scoreProps(item.Score),
		})
	}
	return out
}
