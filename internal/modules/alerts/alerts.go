// Package alerts owns alert rules ("good window" notifications) and
// delivery de-duplication via alert_delivery. Alert areas are stored
// coarsely (H3 cell or radius) for privacy; Free is limited to one rule.
package alerts

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("alert rule not found")
	ErrInvalidRule   = errors.New("invalid alert rule")
	ErrPlanLimit     = errors.New("plan limit reached") // Free: 1 alert area
	ErrDeviceInvalid = errors.New("device not found")
	// ErrNoPushToken means the device has no APNs token registered — the
	// delivery is skipped, not failed.
	ErrNoPushToken = errors.New("device has no push token")
	// ErrPushTokenDead means APNs returned 410 — the token is permanently
	// dead; the device's push credentials are cleared and the delivery is
	// dead-lettered rather than retried.
	ErrPushTokenDead = errors.New("apns token unregistered")
)

// MaxDeliveryAttempts bounds claim→fail→reclaim retries per (rule, window).
const MaxDeliveryAttempts = 3

// Rule is an alert_rule row.
type Rule struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	SpotID     *uuid.UUID
	H3Cell     *uint64
	RadiusM    *int
	MinScore   int
	DaysMask   int // bit i = weekday i (0=Sunday)
	StartLocal time.Duration
	EndLocal   time.Duration
	TZ         string
	Enabled    bool
}

// RuleInput is the create/update payload.
type RuleInput struct {
	SpotID     *uuid.UUID
	H3Cell     *uint64
	RadiusM    *int
	MinScore   int
	DaysMask   int
	StartLocal string // "HH:MM" local time
	EndLocal   string
	TZ         string
	Enabled    *bool
}

// Delivery is one fired alert ready to push.
type Delivery struct {
	RuleID      uuid.UUID
	DeviceID    uuid.UUID
	WindowStart time.Time
	SpotID      uuid.UUID
	SpotName    string
	Score       int
}

// Stats summarizes an Evaluate run.
type Stats struct {
	Rules     int
	Evaluated int
	Fired     int
	Sent      int
	Failed    int
}

// Limits maps plan name -> max rules (-1 = unlimited).
var Limits = map[string]int{
	"free": 1,
	"plus": -1,
}

func parseHHMM(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%w: time must be HH:MM", ErrInvalidRule)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// normalize validates and fills defaults for a RuleInput.
func normalize(in RuleInput) (RuleInput, error) {
	if in.SpotID == nil && in.H3Cell == nil {
		return in, fmt.Errorf("%w: spotId or h3Cell required", ErrInvalidRule)
	}
	if in.SpotID != nil && in.H3Cell != nil {
		return in, fmt.Errorf("%w: spotId and h3Cell are mutually exclusive", ErrInvalidRule)
	}
	if in.H3Cell != nil && (in.RadiusM == nil || *in.RadiusM <= 0 || *in.RadiusM > 50000) {
		return in, fmt.Errorf("%w: radiusM must be 1..50000 for h3Cell rules", ErrInvalidRule)
	}
	if in.MinScore < 0 || in.MinScore > 100 {
		return in, fmt.Errorf("%w: minScore must be 0..100", ErrInvalidRule)
	}
	if in.DaysMask <= 0 || in.DaysMask > 127 {
		return in, fmt.Errorf("%w: daysMask must select 1..7 weekdays", ErrInvalidRule)
	}
	if _, err := time.LoadLocation(in.TZ); err != nil {
		return in, fmt.Errorf("%w: unknown timezone", ErrInvalidRule)
	}
	return in, nil
}
