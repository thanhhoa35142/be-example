package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/traffic"
)

type TrafficRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewTrafficRepo(q *Queries, pool *pgxpool.Pool) *TrafficRepo {
	return &TrafficRepo{q: q, pool: pool}
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t.UTC(), Valid: true}
}

func (r *TrafficRepo) UpsertGscDaily(ctx context.Context, row traffic.GscDaily) error {
	return r.q.UpsertTrafficGscDaily(ctx, UpsertTrafficGscDailyParams{
		Date: pgDate(row.Date), SiteUrl: row.SiteURL,
		Clicks: row.Clicks, Impressions: row.Impressions,
		Ctr: row.CTR, Position: row.Position,
	})
}

func (r *TrafficRepo) ReplaceGscDims(ctx context.Context, siteURL, dimension string, rows []traffic.GscDim) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	if err := q.DeleteTrafficGscDims(ctx, DeleteTrafficGscDimsParams{
		SiteUrl: siteURL, Dimension: dimension,
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := q.UpsertTrafficGscDim(ctx, UpsertTrafficGscDimParams{
			SiteUrl: row.SiteURL, Dimension: row.Dimension, Key: row.Key,
			Clicks: row.Clicks, Impressions: row.Impressions,
			Ctr: row.CTR, Position: row.Position,
			PeriodStart: pgDate(row.PeriodStart), PeriodEnd: pgDate(row.PeriodEnd),
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *TrafficRepo) UpsertGa4Daily(ctx context.Context, row traffic.Ga4Daily) error {
	params := UpsertTrafficGa4DailyParams{
		Date: pgDate(row.Date), PropertyID: row.PropertyID,
		Sessions: row.Sessions, TotalUsers: row.TotalUsers, NewUsers: row.NewUsers,
		PageViews: row.PageViews,
	}
	if row.EngagementRate != nil {
		params.EngagementRate = pgtype.Float8{Float64: *row.EngagementRate, Valid: true}
	}
	if row.AvgSessionDurationSecs != nil {
		params.AvgSessionDurationSec = pgtype.Float8{Float64: *row.AvgSessionDurationSecs, Valid: true}
	}
	return r.q.UpsertTrafficGa4Daily(ctx, params)
}

func (r *TrafficRepo) GscDaily(ctx context.Context, since time.Time) ([]traffic.GscDailyPoint, error) {
	rows, err := r.q.ListTrafficGscDaily(ctx, pgDate(since))
	if err != nil {
		return nil, err
	}
	out := make([]traffic.GscDailyPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, traffic.GscDailyPoint{
			Date: row.Date.Time.UTC(), Clicks: row.Clicks, Impressions: row.Impressions,
			CTR: row.Ctr, Position: row.Position,
		})
	}
	return out, nil
}

func (r *TrafficRepo) GscDims(ctx context.Context, dimension string, limit int) ([]traffic.GscDimRow, error) {
	rows, err := r.q.ListTrafficGscDims(ctx, ListTrafficGscDimsParams{
		Dimension: dimension, Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]traffic.GscDimRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, traffic.GscDimRow{
			Key: row.Key, Clicks: row.Clicks, Impressions: row.Impressions,
			CTR: row.Ctr, Position: row.Position,
		})
	}
	return out, nil
}

func (r *TrafficRepo) GscDimWindow(ctx context.Context, dimension string) (*traffic.GscDimWindow, error) {
	row, err := r.q.ListTrafficGscDimWindow(ctx, dimension)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !row.PeriodStart.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &traffic.GscDimWindow{
		Start: row.PeriodStart.Time.UTC(),
		End:   row.PeriodEnd.Time.UTC(),
	}, nil
}

func (r *TrafficRepo) Ga4Daily(ctx context.Context, since time.Time) ([]traffic.Ga4DailyPoint, error) {
	rows, err := r.q.ListTrafficGa4Daily(ctx, pgDate(since))
	if err != nil {
		return nil, err
	}
	out := make([]traffic.Ga4DailyPoint, 0, len(rows))
	for _, row := range rows {
		p := traffic.Ga4DailyPoint{
			Date: row.Date.Time.UTC(), Sessions: row.Sessions, TotalUsers: row.TotalUsers,
			NewUsers: row.NewUsers, PageViews: row.PageViews,
		}
		if row.EngagementRate != 0 {
			v := row.EngagementRate
			p.EngagementRate = &v
		}
		if row.AvgSessionDurationSec != 0 {
			v := row.AvgSessionDurationSec
			p.AvgSessionDurationSecs = &v
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *TrafficRepo) Ga4Totals(ctx context.Context, since time.Time) (traffic.Ga4Totals, error) {
	row, err := r.q.TrafficGa4Totals(ctx, pgDate(since))
	if err != nil {
		return traffic.Ga4Totals{}, err
	}
	return traffic.Ga4Totals{
		Sessions: row.Sessions, TotalUsers: row.TotalUsers,
		NewUsers: row.NewUsers, PageViews: row.PageViews,
	}, nil
}
