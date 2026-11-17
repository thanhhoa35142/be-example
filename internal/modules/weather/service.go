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

// PastHours of history a refresh asks the provider for (and may rewrite).
// Rows older than that are never touched by a refresh; they are removed only
// by retention (retentionDays here, and the purge job). The wet-grass
// estimate needs recent past precipitation, which a forecast-only refresh
// would otherwise only have as the last forecast made before each hour.
const PastHours = 48

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
			points, err := s.provider.Forecast(gctx, lat, lng, now.Add(-PastHours*time.Hour), to)
			if err != nil {
				fail(err)
				return nil
			}
			rows, replaceFrom := buildRows(cellID, points, now, to, s.provider.Name(), fetchedAt)
			if err := s.repo.ReplaceRows(gctx, cellID, replaceFrom, to, rows); err != nil {
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

// buildRows turns provider points into rows for one cell and returns where
// the replace window starts.
//
// Hours in [now, to] are the forecast and are always replaced, exactly as
// before. Past hours (at most PastHours back) are written only as the
// unbroken run of hours with data ending at now-1h, and the replace window
// starts at the oldest of them. A provider that returns no past hours, or
// returns them as nulls (e.g. a self-hosted instance synced with too few
// --past-days), therefore leaves the stored history untouched: the window
// then starts at now, as it always did.
func buildRows(cellID int64, points []Point, now, to time.Time, provider string, fetchedAt time.Time) ([]Row, time.Time) {
	pastFrom := now.Add(-PastHours * time.Hour)
	toRow := func(p Point) Row {
		return Row{
			CellID: cellID, TS: p.TS.UTC(),
			TempC: p.TempC, FeelsLikeC: p.FeelsLikeC, PrecipProb: p.PrecipProb, PrecipMM: p.PrecipMM,
			WindKmh: p.WindKmh, UV: p.UV, Cloud: p.Cloud,
			Provider: provider, FetchedAt: fetchedAt,
		}
	}

	rows := make([]Row, 0, len(points))
	past := make(map[time.Time]Point)
	for _, p := range points {
		ts := p.TS.UTC()
		switch {
		case ts.After(to) || ts.Before(pastFrom):
		case ts.Before(now):
			if ts.Truncate(time.Hour).Equal(ts) && p.hasData() {
				past[ts] = p
			}
		default:
			rows = append(rows, toRow(p))
		}
	}

	replaceFrom := now
	for ts := now.Add(-time.Hour); !ts.Before(pastFrom); ts = ts.Add(-time.Hour) {
		p, ok := past[ts]
		if !ok {
			break
		}
		rows = append(rows, toRow(p))
		replaceFrom = ts
	}
	return rows, replaceFrom
}
