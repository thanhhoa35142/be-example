package digest

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Sender delivers the digest push (APNs). Must return alerts.ErrNoPushToken
// or alerts.ErrPushTokenDead (wrapped or bare) for permanent skips.
type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

// PlanResolver reports the device's billing plan ("free"/"plus").
type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

// Repository is the persistence port for the digest module.
type Repository interface {
	Upsert(ctx context.Context, deviceID uuid.UUID, spotIDs []uuid.UUID, enabled bool) (*Digest, error)
	Get(ctx context.Context, deviceID uuid.UUID) (*Digest, error)
	Delete(ctx context.Context, deviceID uuid.UUID) error
	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)
	// ResolveSpots maps requested ids to active target ids (merged ids resolve
	// to their target). Ids that are unknown or inactive are absent.
	ResolveSpots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)

	// ListCandidates returns enabled digests of live, push-registered devices.
	ListCandidates(ctx context.Context) ([]Candidate, error)
	// Claim sets last_sent_on=localDate iff it still equals prev. False when
	// another runner already claimed the slot.
	Claim(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) (bool, error)
	// Release restores last_sent_on=prev after a transient send failure.
	Release(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) error

	Spots(ctx context.Context, ids []uuid.UUID) ([]SpotInfo, error)
	// Scores returns the spot's hourly scores in [from, to).
	Scores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]HourScore, error)
}
