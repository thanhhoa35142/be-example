package alerts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ScoredSpot struct {
	ID    uuid.UUID
	Name  string
	Kind  string
	Score int
}

type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

type Repository interface {
	ListRules(ctx context.Context, deviceID uuid.UUID) ([]Rule, error)
	GetRule(ctx context.Context, id uuid.UUID) (*Rule, error)

	InsertRuleLimited(ctx context.Context, deviceID uuid.UUID, in RuleInput, start, end time.Duration, maxRules int) (uuid.UUID, error)

	SpotActive(ctx context.Context, spotID uuid.UUID) (bool, error)
	UpdateRule(ctx context.Context, id, deviceID uuid.UUID, in RuleInput, start, end time.Duration) (bool, error)
	DeleteRule(ctx context.Context, id, deviceID uuid.UUID) (bool, error)

	ListEnabledRules(ctx context.Context) ([]Rule, error)

	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)

	SpotScoreAt(ctx context.Context, spotID uuid.UUID, tsHour time.Time) (*ScoredSpot, error)

	BestScoredSpotNear(ctx context.Context, lat, lon float64, radiusM int, tsHour time.Time) (*Delivery, error)

	SweepDeliveries(ctx context.Context) error

	ClaimDelivery(ctx context.Context, d Delivery, windowStart time.Time, maxAttempts int) (bool, error)

	MarkDeliverySent(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int) error

	MarkDeliveryFailed(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int, lastError string) error

	MarkDeliveryDead(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, lastError string) error

	MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error

	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)
}
