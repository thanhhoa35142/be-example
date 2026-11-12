package recommendations

import (
	"context"
	"math"
	"time"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Recommend returns the best-scoring spots around a point at a given hour
// (default: current hour, quantized for edge-cache friendliness).
func (s *Service) Recommend(ctx context.Context, q Query) ([]Item, error) {
	if math.IsNaN(q.Lat) || q.Lat < -90 || q.Lat > 90 ||
		math.IsNaN(q.Lon) || q.Lon < -180 || q.Lon > 180 {
		return nil, ErrInvalidQuery
	}
	if q.RadiusM <= 0 {
		q.RadiusM = DefaultRadiusM
	}
	if q.RadiusM > MaxRadiusM {
		return nil, ErrInvalidQuery
	}
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	q.When = q.When.UTC().Truncate(time.Hour)
	return s.repo.Nearby(ctx, q.Lat, q.Lon, q.RadiusM, q.When, q.Limit)
}
