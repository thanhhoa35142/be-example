package weather

import (
	"context"
	"time"
)

type Row struct {
	CellID     int64
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
	WindKmh    *float64
	UV         *float64
	Cloud      *int
	Provider   string
	FetchedAt  time.Time
}

type Repository interface {
	DistinctCells(ctx context.Context) ([]int64, error)

	ReplaceRows(ctx context.Context, cellID int64, from, to time.Time, rows []Row) error

	DeleteBefore(ctx context.Context, before time.Time) error

	ReserveQuota(ctx context.Context, provider string, weight, limit int) (bool, error)
}
