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

func (s *Service) Recommend(ctx context.Context, q Query) ([]Item, error) {
	if math.IsNaN(q.Lat) || q.Lat < -90 || q.Lat > 90 ||
		math.IsNaN(q.Lon) || q.Lon < -180 || q.Lon > 180 {
		return nil, ErrInvalidQuery
	}
	if math.IsNaN(q.RadiusM) || math.IsInf(q.RadiusM, 0) {
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
	now := time.Now().UTC()
	q.When = q.When.UTC().Truncate(time.Hour)
	// Bound the forecast window — scores only exist ~7 days out; anything
	// further is an invalid query, not an empty result worth caching.
	if q.When.Before(now.Add(-24*time.Hour)) || q.When.After(now.Add(8*24*time.Hour)) {
		return nil, ErrInvalidQuery
	}
	return s.repo.Nearby(ctx, q.Lat, q.Lon, q.RadiusM, q.When, q.Limit, q.Filter)
}
