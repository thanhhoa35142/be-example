package spots

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxBBoxDeltaDeg caps the viewport so zoomed-out requests stay cheap.
	// 0.5° is roughly 55 km at the equator — city-pack scale.
	MaxBBoxDeltaDeg = 0.5

	// ForecastDays is the spec'd 7-day hourly forecast window.
	ForecastDays = 7
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListInBBox returns active spots in the viewport with the score at the
// requested hour (quantized to the hour for edge-cache friendliness).
func (s *Service) ListInBBox(ctx context.Context, bbox BBox, when *time.Time) ([]SpotWithScore, error) {
	if err := validateBBox(bbox); err != nil {
		return nil, err
	}
	ts := time.Now().UTC()
	if when != nil {
		ts = when.UTC()
	}
	ts = ts.Truncate(time.Hour)
	return s.repo.ListInBBox(ctx, bbox, ts)
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
