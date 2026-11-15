package weather

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"golang.org/x/sync/errgroup"
)

// ForecastDays is 8, not the product's 7: one extra day of buffer for
// timezone edge cases. Nothing reads beyond 7 days (scoring horizon,
// /v1 forecast, recommendations, feedback window).
const ForecastDays = 8

const retentionDays = 14

const maxConcurrentFetches = 8

type Service struct {
	repo       Repository
	provider   Provider
	dailyLimit int
}

func NewService(repo Repository, provider Provider, dailyCallLimit int) *Service {
	return &Service{repo: repo, provider: provider, dailyLimit: dailyCallLimit}
}

type Stats struct {
	Cells   int
	Fetched int

	Replaced int
	Failed   int
}

func (s *Service) Refresh(ctx context.Context) (Stats, error) {
	spotCells, err := s.repo.DistinctCells(ctx)
	if err != nil {
		return Stats{}, err
	}

	cellSet := make(map[int64]struct{}, len(spotCells))
	for _, id := range spotCells {
		cellSet[geo.WeatherCellID(id)] = struct{}{}
	}
	cells := make([]int64, 0, len(cellSet))
	for id := range cellSet {
		cells = append(cells, id)
	}

	if s.dailyLimit > 0 {
		ok, err := s.repo.ReserveQuota(ctx, s.provider.Name(), len(cells), s.dailyLimit)
		if err != nil {
			return Stats{}, err
		}
		if !ok {
			slog.Warn("weather: daily quota exhausted, skipping refresh",
				"provider", s.provider.Name(), "cells", len(cells), "limit", s.dailyLimit)
			// Surface as a job failure — a silent skip leaves weather stale
			// for hours while t_job_runs shows a healthy run.
			return Stats{Cells: len(cells)}, fmt.Errorf("weather: daily quota exhausted (%d cells, limit %d)", len(cells), s.dailyLimit)
		}
	}

	now := time.Now().UTC().Truncate(time.Hour)
	to := now.Add(ForecastDays * 24 * time.Hour)
	fetchedAt := time.Now().UTC()

	var st Stats
	st.Cells = len(cells)
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentFetches)
	for _, cellID := range cells {
		g.Go(func() error {
			fail := func(err error) {
				mu.Lock()
				st.Failed++
				mu.Unlock()
				slog.Warn("weather: cell failed", "cell_id", cellID, "err", err)
			}
			lat, lng, err := geo.CellCenter(cellID)
			if err != nil {
				fail(err)
				return nil
			}
			points, err := s.provider.Forecast(gctx, lat, lng, now, to)
			if err != nil {
				fail(err)
				return nil
			}
			rows := make([]Row, 0, len(points))
			for _, p := range points {
				if p.TS.Before(now) || p.TS.After(to) {
					continue
				}
				rows = append(rows, Row{
					CellID: cellID, TS: p.TS.UTC(),
					TempC: p.TempC, FeelsLikeC: p.FeelsLikeC, PrecipProb: p.PrecipProb,
					WindKmh: p.WindKmh, UV: p.UV, Cloud: p.Cloud,
					Provider: s.provider.Name(), FetchedAt: fetchedAt,
				})
			}
			if err := s.repo.ReplaceRows(gctx, cellID, now, to, rows); err != nil {
				fail(err)
				return nil
			}
			mu.Lock()
			st.Fetched += len(rows)
			st.Replaced++
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()

	// A majority of cells failing means the provider (or our quota) is broken
	// — report the run as failed instead of "succeeded" with stale weather.
	if st.Cells > 0 && st.Failed*2 > st.Cells {
		return st, fmt.Errorf("weather: %d of %d cells failed", st.Failed, st.Cells)
	}

	if err := s.repo.DeleteBefore(ctx, now.Add(-retentionDays*24*time.Hour)); err != nil {
		return st, err
	}
	return st, nil
}
