package http

import (
	"encoding/json"
	"math"
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

// toFeatureCollection renders the viewport/batch result. withArea / withScores
// add the optional `area` / `scores` properties (absent otherwise); both are
// pre-serialized JSON from the query, so `area` is an explicit null for point
// spots and `scores` is [] when no hour has a score.
func toFeatureCollection(items []spots.SpotWithScore, when time.Time, withArea, withScores bool) geoJSONFeatureCollection {
	features := make([]geoJSONFeature, 0, len(items))
	for _, item := range items {
		props := map[string]any{
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
		}
		if withArea {
			if len(item.Area) > 0 {
				props["area"] = item.Area
			} else {
				props["area"] = nil
			}
		}
		if withScores {
			if len(item.HourlyScores) > 0 {
				props["scores"] = item.HourlyScores
			} else {
				props["scores"] = json.RawMessage("[]")
			}
		}
		features = append(features, geoJSONFeature{
			Type: "Feature",
			Geometry: geoJSONPoint{
				Type:        "Point",
				Coordinates: []float64{item.Lon, item.Lat},
			},
			Properties: props,
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
	// WeatherSource: "weatherkit" | "openmeteo" | null (no weather yet).
	WeatherSource *string `json:"weatherSource"`
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
		WeatherSource:  d.WeatherSource,
	}
}

type packItem struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Country     string     `json:"country"`
	TZ          string     `json:"tz"`
	Center      [2]float64 `json:"center"`
	BBox        [4]float64 `json:"bbox"`
	ActiveSpots int        `json:"activeSpots"`
}

func toPackItems(packs []spots.Pack) []packItem {
	out := make([]packItem, 0, len(packs))
	for _, p := range packs {
		out = append(out, packItem{
			Slug: p.Slug, Name: p.Name, Country: p.Country, TZ: p.TZ,
			Center:      [2]float64{round4(p.Center[0]), round4(p.Center[1])},
			BBox:        [4]float64{round4(p.BBox[0]), round4(p.BBox[1]), round4(p.BBox[2]), round4(p.BBox[3])},
			ActiveSpots: p.ActiveSpots,
		})
	}
	return out
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
