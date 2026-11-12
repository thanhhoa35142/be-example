package weather

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"golang.org/x/sync/errgroup"
)

// ForecastDays: Open-Meteo free tier allows ~15 days of hourly forecast.
// Spec keeps ~14 days of weather_hourly anyway.
const ForecastDays = 15

const retentionDays = 14

// maxConcurrentFetches bounds in-flight provider requests. The provider's
// rate limiter (httpx) controls request rate; this only caps parallelism —
// sized so rps × latency stays well under the limit.
const maxConcurrentFetches = 8

type Service struct {
	repo       Repository
	provider   Provider
	dailyLimit int // max provider calls per day; <=0 disables the budget check
}

func NewService(repo Repository, provider Provider, dailyCallLimit int) *Service {
	return &Service{repo: repo, provider: provider, dailyLimit: dailyCallLimit}
}

type Stats struct {
	Cells   int
	Fetched int
	Failed  int
}

// Refresh fetches forecasts for every distinct weather cell covering active
// spots and upserts weather_hourly. Spot cells (res 7) are mapped to coarser
// weather cells (geo.WeatherResolution) first — forecast models resolve at
// ~10 km, so one provider call per weather cell is enough and saves quota.
// One provider call per weather cell per run.
func (s *Service) Refresh(ctx context.Context) (Stats, error) {
	spotCells, err := s.repo.DistinctCells(ctx)
	if err != nil {
		return Stats{}, err
	}

	// Dedupe spot cells onto the coarser weather grid.
	cellSet := make(map[int64]struct{}, len(spotCells))
	for _, id := range spotCells {
		cellSet[geo.WeatherCellID(id)] = struct{}{}
	}
	cells := make([]int64, 0, len(cellSet))
	for id := range cellSet {
		cells = append(cells, id)
	}

	// Daily quota is the real constraint (e.g. Open-Meteo ~10k calls/day),
	// not per-second rate. Consume the whole batch budget atomically up
	// front; on exhaustion skip the run — the next tick retries tomorrow's
	// window naturally.
	if s.dailyLimit > 0 {
		ok, err := s.repo.ConsumeQuota(ctx, s.provider.Name(), len(cells), s.dailyLimit)
		if err != nil {
			return Stats{}, err
		}
		if !ok {
			slog.Warn("weather: daily quota exhausted, skipping refresh",
				"provider", s.provider.Name(), "cells", len(cells), "limit", s.dailyLimit)
			return Stats{Cells: len(cells)}, nil
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
				return nil // bad cell data — skip, don't fail the job
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
			mu.Unlock()
			return nil
		})
	}
	// Goroutines never return errors — per-cell failures are counted in
	// Stats.Failed so one bad cell can't sink the whole refresh.
	_ = g.Wait()

	// Retention: drop rows older than the forecast window.
	if err := s.repo.DeleteBefore(ctx, now.Add(-retentionDays*24*time.Hour)); err != nil {
		return st, err
	}
	return st, nil
}
