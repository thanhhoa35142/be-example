package weather

import (
	"context"
	"time"
)

// Row is a weather_hourly record ready for persistence.
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

// Repository is the persistence port for the weather module.
type Repository interface {
	// DistinctCells returns cell_ids that currently have active spots.
	DistinctCells(ctx context.Context) ([]int64, error)
	// ReplaceRows replaces all rows for a cell within [from, to) — refresh semantics.
	ReplaceRows(ctx context.Context, cellID int64, from, to time.Time, rows []Row) error
	// DeleteBefore removes rows older than the cutoff (retention ~14 days).
	DeleteBefore(ctx context.Context, before time.Time) error
	// ReserveQuota atomically adds weight to the provider's UTC-day budget.
	// Returns false when the budget would be exceeded.
	ReserveQuota(ctx context.Context, provider string, weight, limit int) (bool, error)
}
