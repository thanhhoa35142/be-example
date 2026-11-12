package persistence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thanhhoa35142/be-example/internal/modules/weather"
)

type WeatherRepo struct {
	q *Queries
}

func NewWeatherRepo(q *Queries) *WeatherRepo {
	return &WeatherRepo{q: q}
}

func f4(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

func i2(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

func (r *WeatherRepo) DistinctCells(ctx context.Context) ([]int64, error) {
	return r.q.DistinctSpotCells(ctx)
}

func (r *WeatherRepo) ReplaceRows(ctx context.Context, cellID int64, from, to time.Time, rows []weather.Row) error {
	if err := r.q.DeleteWeatherCellRange(ctx, DeleteWeatherCellRangeParams{
		CellID: cellID, Ts: from, Ts_2: to,
	}); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	params := make([]UpsertWeatherHourlyParams, len(rows))
	for i, row := range rows {
		params[i] = UpsertWeatherHourlyParams{
			CellID: row.CellID, Ts: row.TS,
			Temp: f4(row.TempC), FeelsLike: f4(row.FeelsLikeC),
			PrecipProb: i2(row.PrecipProb), Wind: f4(row.WindKmh),
			Uv: f4(row.UV), Cloud: i2(row.Cloud),
			Provider: row.Provider, FetchedAt: row.FetchedAt,
		}
	}
	_, err := r.q.UpsertWeatherHourly(ctx, params)
	return err
}

func (r *WeatherRepo) DeleteBefore(ctx context.Context, before time.Time) error {
	return r.q.DeleteWeatherBefore(ctx, before)
}
