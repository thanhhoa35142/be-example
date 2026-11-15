package recommendations

import (
	"errors"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

var (
	ErrInvalidQuery = errors.New("invalid query")
)

const (
	DefaultRadiusM = 5000.0
	MaxRadiusM     = 50000.0

	DefaultLimit = 3
	MaxLimit     = 50
)

type Query struct {
	Lat     float64
	Lon     float64
	RadiusM float64
	When    time.Time
	Limit   int
	Filter  spots.Filter
}

type Item struct {
	spots.Spot
	DistanceM float64
	Score     *spots.ScoreSnapshot
}
