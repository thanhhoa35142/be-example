package nudge

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Sender delivers one nudge. It returns alerts.ErrNoPushToken or
// alerts.ErrPushTokenDead for a device that cannot be pushed to.
type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

type Repository interface {
	Upsert(ctx context.Context, in SubscriptionUpsert) (*Subscription, error)
	Get(ctx context.Context, deviceID uuid.UUID) (*Subscription, error)
	Delete(ctx context.Context, deviceID uuid.UUID) error
	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)

	// Sweep turns stale claims into failed and exhausted failures into dead.
	Sweep(ctx context.Context, now time.Time) error
	Candidates(ctx context.Context, now time.Time) ([]Candidate, error)
	// ScoresNear returns hourly scores in [from, to) of the best spots around
	// the cell, ranked by quality then distance.
	ScoresNear(ctx context.Context, cell int64, radiusM int, from, to time.Time) ([]SpotHour, error)

	// Claim reserves (device, local day). False when the day already has a
	// delivery.
	Claim(ctx context.Context, d Delivery, now time.Time) (bool, error)
	// Reclaim retries a failed delivery of the same local day while attempts
	// remain.
	Reclaim(ctx context.Context, d Delivery, now time.Time) (bool, error)
	// MarkSent flips the delivery to sent and folds the ignored streak and
	// last_sent_at into the subscription in one transaction.
	MarkSent(ctx context.Context, d Delivery, sentAt time.Time, ignoredStreak int) error
	MarkFailed(ctx context.Context, d Delivery, reason string) error
	MarkDead(ctx context.Context, d Delivery, reason string) error
	// RecordPushSend writes the cross-kind frequency-cap ledger row.
	RecordPushSend(ctx context.Context, deviceID uuid.UUID, kind string, sentAt time.Time) error
}
