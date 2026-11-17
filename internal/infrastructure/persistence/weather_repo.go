package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhhoa35142/be-example/internal/modules/weather"
)

type WeatherRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewWeatherRepo(q *Queries, pool *pgxpool.Pool) *WeatherRepo {
	return &WeatherRepo{q: q, pool: pool}
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
	// An empty forecast must not wipe the existing rows — keep the previous
	// data and let retention clean it up instead.
	if len(rows) == 0 {
		return nil
	}
	// Delete + insert in one transaction: a failure between them would
	// otherwise leave the cell with no forecast until the next refresh.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	if err := q.DeleteWeatherCellRange(ctx, DeleteWeatherCellRangeParams{
		CellID: cellID, Ts: from, Ts_2: to,
	}); err != nil {
		return err
	}
	params := make([]UpsertWeatherHourlyParams, len(rows))
	for i, row := range rows {
		params[i] = UpsertWeatherHourlyParams{
			CellID: row.CellID, Ts: row.TS,
			Temp: f4(row.TempC), FeelsLike: f4(row.FeelsLikeC),
			PrecipProb: i2(row.PrecipProb), Wind: f4(row.WindKmh),
			Uv: f4(row.UV), Cloud: i2(row.Cloud),
			Provider: row.Provider, FetchedAt: row.FetchedAt,
			PrecipMm: f4(row.PrecipMM),
		}
	}
	if _, err := q.UpsertWeatherHourly(ctx, params); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *WeatherRepo) DeleteBefore(ctx context.Context, before time.Time) error {
	return r.q.DeleteWeatherBefore(ctx, before)
}

func (r *WeatherRepo) ReserveQuota(ctx context.Context, provider string, weight, limit int) (bool, error) {
	_, err := r.q.ReserveProviderQuota(ctx, ReserveProviderQuotaParams{
		Provider: provider, Weight: int32(weight), QuotaLimit: int32(limit),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
