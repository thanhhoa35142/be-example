package spots

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

const (
	// MaxBBoxDeltaDeg caps the viewport so zoomed-out requests stay cheap.
	// 0.5° is roughly 55 km at the equator — city-pack scale.
	MaxBBoxDeltaDeg = 0.5

	// ForecastDays is the spec'd 7-day hourly forecast window.
	ForecastDays = 7

	// MaxBatchIDs caps GET /v1/spots?ids=.
	MaxBatchIDs = 50
	// MaxScoreHours caps the hourly series (GET /v1/spots?hours=).
	MaxScoreHours = 24
)

// WeatherSourceCode maps a stored provider name to the public attribution
// code the app renders ("Apple Weather" / "Open-Meteo"); nil for none/unknown.
func WeatherSourceCode(provider string) *string {
	var code string
	switch provider {
	case "weatherkit":
		code = "weatherkit"
	case "open-meteo", "openmeteo":
		code = "openmeteo"
	default:
		return nil
	}
	return &code
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListInBBox returns active spots in the viewport with the score at the
// requested hour (quantized to the hour for edge-cache friendliness).
func (s *Service) ListInBBox(ctx context.Context, bbox BBox, when *time.Time) ([]SpotWithScore, error) {
	return s.List(ctx, ListQuery{BBox: &bbox, When: when})
}

// List serves GET /v1/spots: by viewport or by ids, optionally with the
// simplified outline and an hourly score series.
func (s *Service) List(ctx context.Context, q ListQuery) ([]SpotWithScore, error) {
	if (q.BBox == nil) == (len(q.IDs) == 0) {
		return nil, ErrInvalidQuery
	}
	if q.BBox != nil {
		if err := validateBBox(*q.BBox); err != nil {
			return nil, err
		}
	}
	if len(q.IDs) > MaxBatchIDs || q.Hours < 0 || q.Hours > MaxScoreHours {
		return nil, ErrInvalidQuery
	}
	now := time.Now().UTC()
	ts := now
	switch {
	case q.When != nil:
		ts = q.When.UTC()
	case q.From != nil:
		ts = q.From.UTC()
	}
	ts = ts.Truncate(time.Hour)

	var from, to time.Time // empty range unless an hourly series is wanted
	if q.Hours > 0 {
		from = now
		if q.From != nil {
			from = q.From.UTC()
		}
		from = from.Truncate(time.Hour)
		to = from.Add(time.Duration(q.Hours) * time.Hour)
	}
	return s.repo.List(ctx, q, ts, from, to)
}

// ListPacks returns the city packs the app can use (beta/live).
func (s *Service) ListPacks(ctx context.Context) ([]Pack, error) {
	return s.repo.ListPacks(ctx)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (SpotDetail, error) {
	detail, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	switch detail.Status {
	case "merged":
		if detail.MergedInto != nil {
			return SpotDetail{}, MergedError{NewID: *detail.MergedInto}
		}
		return SpotDetail{}, ErrNotFound
	case "active":
	default:
		return SpotDetail{}, ErrNotFound
	}

	detail.Amenities, err = s.repo.ListAmenities(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	detail.Sources, err = s.repo.ListSources(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	// Weather rows live on the coarser weather grid, like the scorer reads them.
	provider, err := s.repo.WeatherProvider(ctx, geo.WeatherCellID(detail.CellID))
	if err != nil {
		return SpotDetail{}, err
	}
	detail.WeatherSource = WeatherSourceCode(provider)
	return detail, nil
}

// Forecast returns hourly scores for the next ForecastDays days starting at
// the current hour.
func (s *Service) Forecast(ctx context.Context, id uuid.UUID) ([]ForecastPoint, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	from := time.Now().UTC().Truncate(time.Hour)
	to := from.Add(ForecastDays * 24 * time.Hour)
	points, err := s.repo.ListForecast(ctx, id, from, to)
	if err != nil {
		return nil, err
	}
	for i := range points {
		points[i].TSHour = points[i].TSHour.UTC()
	}
	return points, nil
}

func validateBBox(b BBox) error {
	if b.West >= b.East || b.South >= b.North {
		return ErrInvalidBBox
	}
	if b.West < -180 || b.East > 180 || b.South < -90 || b.North > 90 {
		return ErrInvalidBBox
	}
	if b.East-b.West > MaxBBoxDeltaDeg || b.North-b.South > MaxBBoxDeltaDeg {
		return ErrBboxTooLarge
	}
	return nil
}
