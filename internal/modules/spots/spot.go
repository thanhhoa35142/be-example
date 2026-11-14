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
	// Area is the simplified outline (GeoJSON MultiPolygon), only loaded when
	// ListQuery.IncludeArea is set; nil for point spots.
	Area json.RawMessage
	// HourlyScores is a pre-serialized JSON array of {tsHour, score, capReason},
	// only loaded when ListQuery.Hours > 0; nil when no hour has a score.
	HourlyScores json.RawMessage
}

// ListQuery selects spots either by viewport or by id (exactly one of BBox /
// IDs). Scores are read at When (default From, default now).
type ListQuery struct {
	BBox        *BBox
	IDs         []uuid.UUID
	When        *time.Time
	From        *time.Time // start of the hourly series; used with Hours
	Hours       int        // 0 = no hourly series, else 1..MaxScoreHours
	IncludeArea bool
	Filter      Filter
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
	// Weather is the raw forecast behind the score for this hour; nil when
	// the hour has no weather row.
	Weather *HourWeather `json:"weather,omitempty"`
}

// HourWeather is the provider forecast for one hour (metric units; the
// client converts for display). Absent keys mean the provider had no value.
type HourWeather struct {
	TempC      *float64 `json:"tempC,omitempty"`
	FeelsLikeC *float64 `json:"feelsLikeC,omitempty"`
	PrecipProb *int     `json:"precipProb,omitempty"` // 0-100 %
	WindKmh    *float64 `json:"windKmh,omitempty"`
	UV         *float64 `json:"uv,omitempty"`
	Cloud      *int     `json:"cloud,omitempty"` // 0-100 %
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
	Status string `json:"-"`
	CellID int64  `json:"-"`
	// WeatherSource is the provider of the weather behind the scores
	// ("weatherkit" | "openmeteo"); nil when the spot has no weather yet.
	WeatherSource *string         `json:"-"`
	MergedInto    *uuid.UUID      `json:"-"`
	AreaGeoJSON   json.RawMessage `json:"area,omitempty"`
	Amenities     []Amenity       `json:"amenities"`
	Sources       []Source        `json:"sources"`
}

// Pack is a city pack as listed to the app.
type Pack struct {
	Slug        string
	Name        string
	Country     string
	TZ          string
	Center      [2]float64 // lon, lat
	BBox        [4]float64 // west, south, east, north
	ActiveSpots int
}
