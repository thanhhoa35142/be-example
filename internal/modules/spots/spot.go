package spots

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	KindTable Kind = "table"
	KindSite  Kind = "site"
	KindPark  Kind = "park"
	KindBeach Kind = "beach"
)

// BBox is a viewport in WGS84 degrees: west, south, east, north.
type BBox struct {
	West  float64
	South float64
	East  float64
	North float64
}

// Names maps language codes (OSM name:xx tags) to localized names.
type Names map[string]string

// Reason is a score explanation code with parameters; the app translates it.
type Reason struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

type Spot struct {
	ID             uuid.UUID
	CityPackID     uuid.UUID
	Kind           Kind
	Name           *string
	Names          Names
	Lon            float64
	Lat            float64
	Country        string
	City           *string
	TZ             string
	LastVerifiedAt *time.Time
}

// ScoreSnapshot is the precomputed score for one spot at one hour.
type ScoreSnapshot struct {
	Score      *int
	Confidence *int
	CapReason  *string
	Reasons    []Reason
	ComputedAt *time.Time
}

type SpotWithScore struct {
	Spot
	Score *ScoreSnapshot
}

type Components struct {
	Weather *int `json:"weather,omitempty"`
	Crowd   *int `json:"crowd,omitempty"`
	Amenity *int `json:"amenity,omitempty"`
	Shade   *int `json:"shade,omitempty"`
	Fresh   *int `json:"fresh,omitempty"`
}

type ForecastPoint struct {
	TSHour     time.Time  `json:"tsHour"`
	Score      int        `json:"score"`
	Components Components `json:"components"`
	CapReason  *string    `json:"capReason,omitempty"`
	Reasons    []Reason   `json:"reasons"`
	Confidence int        `json:"confidence"`
}

type Amenity struct {
	Type      string `json:"type"`
	DistanceM int    `json:"distanceM"`
	Source    string `json:"source"`
}

type Source struct {
	Name       string    `json:"source"`
	ID         string    `json:"sourceId"`
	Version    *int      `json:"version,omitempty"`
	ImportedAt time.Time `json:"importedAt"`
}

type SpotDetail struct {
	Spot
	Status      string          `json:"-"`
	MergedInto  *uuid.UUID      `json:"-"`
	AreaGeoJSON json.RawMessage `json:"area,omitempty"`
	Amenities   []Amenity       `json:"amenities"`
	Sources     []Source        `json:"sources"`
}
