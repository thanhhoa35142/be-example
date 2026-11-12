package alerts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ScoredSpot is a spot with its score at an hour.
type ScoredSpot struct {
	ID    uuid.UUID
	Name  string
	Score int
}

// Sender delivers a fired alert to the device (APNs). Implemented by the
// notifier module; a no-op/log implementation is fine for development.
// Send must return ErrNoPushToken (wrapped or bare) when the device has no
// reachable push token so the caller can skip it without counting a failure.
type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

// PlanResolver reports the device's billing plan ("free"/"plus").
// Implemented by the entitlement module.
type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

// Repository is the persistence port for the alerts module.
type Repository interface {
	ListRules(ctx context.Context, deviceID uuid.UUID) ([]Rule, error)
	GetRule(ctx context.Context, id uuid.UUID) (*Rule, error)
	CountRules(ctx context.Context, deviceID uuid.UUID) (int, error)
	InsertRule(ctx context.Context, deviceID uuid.UUID, in RuleInput, start, end time.Duration) (uuid.UUID, error)
	UpdateRule(ctx context.Context, id, deviceID uuid.UUID, in RuleInput, start, end time.Duration) (bool, error)
	DeleteRule(ctx context.Context, id, deviceID uuid.UUID) (bool, error)

	// ListEnabledRules returns all enabled rules for evaluation.
	ListEnabledRules(ctx context.Context) ([]Rule, error)
	// DeviceAPNsToken returns the token/env/locale for push, or ok=false.
	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)
	// SpotScoreAt returns the spot's name+score at an hour.
	// Returns ErrNotFound when the spot or score row is missing.
	SpotScoreAt(ctx context.Context, spotID uuid.UUID, tsHour time.Time) (*ScoredSpot, error)
	// BestScoredSpotNear returns the highest-scoring spot within radius of a point.
	BestScoredSpotNear(ctx context.Context, lat, lon float64, radiusM int, tsHour time.Time) (*Delivery, error)
	// SweepDeliveries fails stale 'claimed' rows and dead-letters 'failed'
	// rows whose attempts are exhausted. Best-effort hygiene per evaluate run.
	SweepDeliveries(ctx context.Context) error
	// ClaimDelivery claims the (rule, window) slot for sending: inserts a
	// 'claimed' row, or re-claims a 'failed' one while attempts remain.
	// Returns false when the slot is already claimed/sent/dead or retries
	// are exhausted.
	ClaimDelivery(ctx context.Context, d Delivery, windowStart time.Time, maxAttempts int) (bool, error)
	// MarkDeliverySent records a successful send (claimed → sent).
	MarkDeliverySent(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int) error
	// MarkDeliveryFailed records a failed send attempt (claimed → failed).
	MarkDeliveryFailed(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int, lastError string) error
	// MarkDeliveryDead terminally fails a delivery (failed → dead), e.g. on
	// APNs 410 where retrying can never succeed.
	MarkDeliveryDead(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, lastError string) error
	// MarkDeviceUnregistered clears push credentials after APNs 410.
	MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error
	// DeviceExists reports whether an active device exists.
	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)
}
