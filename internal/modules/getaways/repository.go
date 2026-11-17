package getaways

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	// ActiveCaps returns the cap values of the active scoring config.
	ActiveCaps(ctx context.Context) (Caps, error)
	// Packs lists beta/live packs with their active-spot counts.
	Packs(ctx context.Context) ([]Pack, error)
	// ComputeHours computes the city-hour series of one pack for start hours
	// 08-19 local in [from, to): median of LEAST(s_weather, cap) over active
	// spots, rain share, spot count and the top spot by total score.
	ComputeHours(ctx context.Context, packID uuid.UUID, caps Caps, from, to time.Time) ([]CityHour, error)
	// ReplaceHours atomically replaces the pack's stored hours at or after from.
	ReplaceHours(ctx context.Context, packID uuid.UUID, from time.Time, hours []CityHour, configVersion int16, computedAt time.Time) error
	// DeleteExceptPacks removes stored hours of every pack not in keep.
	DeleteExceptPacks(ctx context.Context, keep []uuid.UUID) (int64, error)
	// DeleteBefore removes stored hours older than the cutoff.
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)

	// Outlook returns stored hours of beta/live packs whose local date is in
	// [fromDate, toDate] and that start at or after notBefore.
	Outlook(ctx context.Context, fromDate, toDate, notBefore time.Time) ([]OutlookRow, error)
	// Centers returns [lon, lat] points on the packs' boundaries.
	Centers(ctx context.Context, packIDs []uuid.UUID) (map[uuid.UUID][2]float64, error)
	Spots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]SpotInfo, error)
	// SpotReasons returns the stored score reasons of a spot at an hour; nil
	// when the spot has no score row there.
	SpotReasons(ctx context.Context, spotID uuid.UUID, tsHour time.Time) ([]Reason, error)
}
