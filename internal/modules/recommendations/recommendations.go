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
	// DefaultRadiusM / MaxRadiusM bound the search area.
	DefaultRadiusM = 5000.0
	MaxRadiusM     = 50000.0

	// DefaultLimit matches the Free tier "best now" = 3 results.
	// MaxLimit caps abuse; Plus tier (unlimited) arrives with entitlement.
	DefaultLimit = 3
	MaxLimit     = 50
)

// Query is the validated input for recommendations.
type Query struct {
	Lat     float64
	Lon     float64
	RadiusM float64
	When    time.Time // already quantized to the hour
	Limit   int
}

// Item is one recommended spot with its score at Query.When.
type Item struct {
	spots.Spot
	DistanceM float64
	Score     *spots.ScoreSnapshot
}
