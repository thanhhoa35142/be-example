package http

import (
	"encoding/json"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type geoJSONPoint struct {
	Type        string    `json:"type"` // "Point"
	Coordinates []float64 `json:"coordinates"`
}

type geoJSONFeature struct {
	Type       string         `json:"type"` // "Feature"
	Geometry   geoJSONPoint   `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type geoJSONFeatureCollection struct {
	Type     string           `json:"type"` // "FeatureCollection"
	Features []geoJSONFeature `json:"features"`
}

func toFeatureCollection(items []spots.SpotWithScore, when time.Time) geoJSONFeatureCollection {
	features := make([]geoJSONFeature, 0, len(items))
	for _, item := range items {
		features = append(features, geoJSONFeature{
			Type: "Feature",
			Geometry: geoJSONPoint{
				Type:        "Point",
				Coordinates: []float64{item.Lon, item.Lat},
			},
			Properties: map[string]any{
				"id":             item.ID,
				"kind":           item.Kind,
				"name":           item.Name,
				"names":          item.Names,
				"country":        item.Country,
				"city":           item.City,
				"tz":             item.TZ,
				"lastVerifiedAt": utcPtr(item.LastVerifiedAt),
				"when":           when.UTC().Format(time.RFC3339),
				"score":          scoreProps(item.Score),
			},
		})
	}
	return geoJSONFeatureCollection{Type: "FeatureCollection", Features: features}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func scoreProps(s *spots.ScoreSnapshot) map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"score":      s.Score,
		"confidence": s.Confidence,
		"capReason":  s.CapReason,
		"reasons":    s.Reasons,
		"computedAt": utcPtr(s.ComputedAt),
	}
}

// detailResponse is the JSON shape for GET /v1/spots/{id}.
type detailResponse struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Name           *string         `json:"name"`
	Names          spots.Names     `json:"names"`
	Coordinates    [2]float64      `json:"coordinates"`
	Area           json.RawMessage `json:"area,omitempty"`
	Country        string          `json:"country"`
	City           *string         `json:"city"`
	TZ             string          `json:"tz"`
	LastVerifiedAt *time.Time      `json:"lastVerifiedAt"`
	Amenities      []spots.Amenity `json:"amenities"`
	Sources        []spots.Source  `json:"sources"`
}

func toDetailResponse(d spots.SpotDetail) detailResponse {
	sources := make([]spots.Source, len(d.Sources))
	for i, src := range d.Sources {
		src.ImportedAt = src.ImportedAt.UTC()
		sources[i] = src
	}
	return detailResponse{
		ID:             d.ID.String(),
		Kind:           string(d.Kind),
		Name:           d.Name,
		Names:          d.Names,
		Coordinates:    [2]float64{d.Lon, d.Lat},
		Area:           d.AreaGeoJSON,
		Country:        d.Country,
		City:           d.City,
		TZ:             d.TZ,
		LastVerifiedAt: utcPtr(d.LastVerifiedAt),
		Amenities:      d.Amenities,
		Sources:        sources,
	}
}
