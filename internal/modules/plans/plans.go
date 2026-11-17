// Package plans holds server-stored picnic plans (C2 of
// FEATURE_PLAN_DECISION_PLANS_GRASS.md): a device plans a time window at a
// spot, shares an unguessable token, followers subscribe with it, and
// plans.evaluate pushes owner and followers when the window turns worse or
// bad (or confirms it the evening before).
package plans

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type State string

const (
	StateOK    State = "ok"
	StateWorse State = "worse"
	StateBad   State = "bad"
)

// Limits caps active (not canceled, not ended) plans a device owns, per
// entitlement plan. Mirrored for display in t_plan_limits ('active_plans').
var Limits = map[string]int{"free": 1, "plus": 20}

const (
	// MaxFollowers per plan.
	MaxFollowers = 20
	// MaxFollows is how many active plans one device may follow (self-DoS
	// bound on the evaluator fan-out; not in the plan document).
	MaxFollows = 50
	// MinWindow / MaxWindow bound end - start.
	MinWindow = time.Hour
	MaxWindow = 12 * time.Hour
	// Horizon: the window must end within the scoring horizon.
	Horizon = spots.ForecastDays * 24 * time.Hour
)

var (
	ErrNotFound      = errors.New("plan not found")
	ErrSpotNotFound  = errors.New("spot not found")
	ErrDeviceInvalid = errors.New("device not found")
	ErrPlanLimit     = errors.New("active plan limit reached")
	ErrFollowerLimit = errors.New("plan follower limit reached")
	ErrFollowLimit   = errors.New("followed plan limit reached")
	ErrOwnPlan       = errors.New("cannot follow own plan")
)

// ValidationError carries a reason code for the client (no sentence).
type ValidationError struct{ Code string }

func (e ValidationError) Error() string { return "invalid plan: " + e.Code }

// Reason codes returned as the problem `code` (400).
const (
	CodeStartPast     = "plan_start_past"
	CodeBeyondHorizon = "plan_beyond_horizon"
	CodeWindowInvalid = "plan_window_invalid"
	CodeInvalidTZ     = "invalid_tz"
)

// Alternative is the best option found when a plan turns worse or bad.
// Type "time": another window the same local day at the same spot (Start,
// End). Type "spot": another spot near the planned one at the planned time
// (SpotID, SpotName, SpotKind). Type "none": nothing better.
type Alternative struct {
	Type     string     `json:"type"`
	Start    *time.Time `json:"start,omitempty"`
	End      *time.Time `json:"end,omitempty"`
	SpotID   *uuid.UUID `json:"spotId,omitempty"`
	SpotName string     `json:"spotName,omitempty"`
	SpotKind string     `json:"spotKind,omitempty"`
	Score    int        `json:"score,omitempty"`
}

const (
	AltTime = "time"
	AltSpot = "spot"
	AltNone = "none"
)

// Plan as listed to its owner or a follower.
type Plan struct {
	ID            uuid.UUID
	Role          string // owner | follower
	ShareToken    string
	SpotID        uuid.UUID
	SpotName      string
	SpotKind      string
	Start, End    time.Time
	TZ            string
	Baseline      *int
	State         *State
	Score         *int
	CapReason     *string
	Alternative   *Alternative
	CanceledAt    *time.Time
	CreatedAt     time.Time
	FollowerCount int
}

// Status derives the lifecycle label at now.
func (p Plan) Status(now time.Time) string {
	switch {
	case p.CanceledAt != nil:
		return "canceled"
	case !now.Before(p.End):
		return "ended"
	case !now.Before(p.Start):
		return "started"
	default:
		return "upcoming"
	}
}

// Shared is the anonymous token view: one window, no device data.
type Shared struct {
	SpotID     uuid.UUID
	SpotName   string
	SpotKind   string
	SpotCity   string
	Lat, Lon   float64
	Start, End time.Time
	TZ         string
	Window     Window
	HasWindow  bool
	State      *State
	// baseline is read for classification only; never serialized.
	baseline *int
}

// WithBaseline is for repositories building a Shared row.
func (s *Shared) WithBaseline(b *int) *Shared { s.baseline = b; return s }

type SpotRef struct {
	ID   uuid.UUID
	Name string
	Kind string
	TZ   string
}

type NewPlan struct {
	ShareToken string
	DeviceID   uuid.UUID
	SpotID     uuid.UUID
	Start, End time.Time
	TZ         string
	Baseline   *int
	State      *State
	Score      *int
	CapReason  *string
}

type CreateInput struct {
	SpotID uuid.UUID
	Start  time.Time
	End    time.Time
	TZ     string
}

// tokenBytes of crypto/rand entropy → 22 base64url chars.
const tokenBytes = 16

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// NewShareToken returns 128 random bits, base64url without padding.
func NewShareToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidToken reports whether s has the share-token shape (checked before any
// database lookup).
func ValidToken(s string) bool { return tokenRe.MatchString(s) }

// validate normalizes and checks a create request at now. Times are kept to
// the minute; the evaluated window is every score hour overlapping it.
func validate(in CreateInput, spotTZ string, now time.Time) (CreateInput, *time.Location, error) {
	tz := in.TZ
	if tz == "" {
		tz = spotTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return in, nil, ValidationError{CodeInvalidTZ}
	}
	in.TZ = tz
	in.Start = in.Start.UTC().Truncate(time.Minute)
	in.End = in.End.UTC().Truncate(time.Minute)
	if !in.Start.After(now) {
		return in, nil, ValidationError{CodeStartPast}
	}
	if d := in.End.Sub(in.Start); d < MinWindow || d > MaxWindow {
		return in, nil, ValidationError{CodeWindowInvalid}
	}
	if in.End.After(now.UTC().Truncate(time.Hour).Add(Horizon)) {
		return in, nil, ValidationError{CodeBeyondHorizon}
	}
	return in, loc, nil
}
