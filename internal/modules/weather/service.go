package weather

import (
	"context"
	"log/slog"
	"time"

	"gitlab.com/walkalike-group/pickhour/internal/modules/geo"
)

// ForecastDays: Open-Meteo free tier allows ~15 days of hourly forecast.
// Spec keeps ~14 days of weather_hourly anyway.
const ForecastDays = 15

const retentionDays = 14

type Service struct {
	repo     Repository
	provider Provider
}

func NewService(repo Repository, provider Provider) *Service {
	return &Service{repo: repo, provider: provider}
}

type Stats struct {
	Cells   int
	Fetched int
	Failed  int
}

// Refresh fetches forecasts for every distinct active spot cell and upserts
// weather_hourly. One provider call per cell per run.
func (s *Service) Refresh(ctx context.Context) (Stats, error) {
	cells, err := s.repo.DistinctCells(ctx)
	if err != nil {
		return Stats{}, err
	}

	now := time.Now().UTC().Truncate(time.Hour)
	to := now.Add(ForecastDays * 24 * time.Hour)
	fetchedAt := time.Now().UTC()

	var st Stats
	st.Cells = len(cells)
	for _, cellID := range cells {
		lat, lng, err := geo.CellCenter(cellID)
		if err != nil {
			slog.Warn("weather: skip invalid cell", "cell_id", cellID, "err", err)
			st.Failed++
			continue
		}
		points, err := s.provider.Forecast(ctx, lat, lng, now, to)
		if err != nil {
			slog.Warn("weather: forecast failed", "cell_id", cellID, "err", err)
			st.Failed++
			continue
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
		if err := s.repo.ReplaceRows(ctx, cellID, now, to, rows); err != nil {
			return st, err
		}
		st.Fetched += len(rows)
	}

	// Retention: drop rows older than the forecast window.
	if err := s.repo.DeleteBefore(ctx, now.Add(-retentionDays*24*time.Hour)); err != nil {
		return st, err
	}
	return st, nil
}
