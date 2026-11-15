package digest

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

type Repository interface {
	Upsert(ctx context.Context, deviceID uuid.UUID, spotIDs []uuid.UUID, enabled bool) (*Digest, error)
	Get(ctx context.Context, deviceID uuid.UUID) (*Digest, error)
	Delete(ctx context.Context, deviceID uuid.UUID) error
	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)

	ResolveSpots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)

	ListCandidates(ctx context.Context) ([]Candidate, error)

	Claim(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) (bool, error)

	Release(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) error

	Spots(ctx context.Context, ids []uuid.UUID) ([]SpotInfo, error)

	Scores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]HourScore, error)
}
