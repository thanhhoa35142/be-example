package nudge

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

const (
	// Kind is the payload `kind` and the t_push_sends ledger kind.
	Kind = "nudge.goodday"

	SlotDay     = "day"
	SlotWeekend = "weekend"

	ConsentExplicit    = "explicit"
	ConsentProvisional = "provisional"

	SourceLocation = "location"
	SourceCity     = "city"

	DefaultSendHour = 8
	MinSendHour     = 7
	MaxSendHour     = 10

	// A good day is a 2-4 hour run of hours scoring >= MinHourScore inside the
	// 10:00-19:00 spot-local day.
	MinHourScore = 70
	MinWindow    = 2
	MaxWindow    = 4
	DayStartHour = 10
	DayEndHour   = 19

	// ExceptionalMean makes a day worth a push even when it is not in the
	// cell's top TopDays of the horizon.
	ExceptionalMean = 90.0
	TopDays         = 2
	HorizonDays     = 7

	// The weekend slot fires at this local time on Friday.
	WeekendSendHour = 18

	MaxDeliveryAttempts = alerts.MaxDeliveryAttempts
)

var (
	ErrNotFound      = errors.New("nudge subscription not found")
	ErrInvalid       = errors.New("invalid nudge subscription")
	ErrDeviceInvalid = errors.New("device not found")
)

// Home is the coordinate a client sends. It is converted to a cell and then
// dropped; it is never stored or logged.
type Home struct {
	Lat, Lng float64
	Source   string
}

// PutInput is a partial update. On the first PUT Consent and Home are
// required (Home may follow later: the app can opt in before it has a
// location fix); afterwards every field is optional and stored values are kept.
type PutInput struct {
	Enabled  *bool
	Consent  *string
	Home     *Home
	SendHour *int
}

// Subscription is the stored opt-in. HomeCell never leaves the backend: the
// API only reports whether a home is set.
type Subscription struct {
	DeviceID      uuid.UUID
	Enabled       bool
	HomeCell      *int64
	HomeSource    string     // empty while no home is set
	HomeUpdatedAt *time.Time // nil while no home is set
	SendHour      int
	Consent       string
}

func (s Subscription) HomeSet() bool { return s.HomeCell != nil }

// SubscriptionUpsert is the merged row the repository writes. A nil HomeCell
// keeps the stored cell and its updated-at.
type SubscriptionUpsert struct {
	DeviceID   uuid.UUID
	Enabled    bool
	HomeCell   *int64
	HomeSource string
	SendHour   int
	Consent    string
}

// Candidate is a device that passed every eligibility rule that does not
// depend on its local clock. The service decides the local part.
type Candidate struct {
	DeviceID      uuid.UUID
	TZ            string
	HomeCell      int64
	SendHour      int
	LastSentAt    *time.Time
	IgnoredStreak int // effective streak: ignored nudges before this send

	DigestEnabled    bool
	DigestSentAt     *time.Time // latest digest push in the last 8 days
	WeekendNudgeDate *time.Time // local_date of the latest sent weekend nudge

	LastDeliveryDate     *time.Time
	LastDeliveryStatus   string
	LastDeliveryAttempts int
}

// SpotHour is one hourly score of a spot near a cell.
type SpotHour struct {
	SpotID    uuid.UUID
	Name      string
	TZ        string
	Quality   int
	DistanceM float64
	Start     time.Time
	Score     int
}

// Delivery is one nudge for one device on one local day.
type Delivery struct {
	DeviceID    uuid.UUID
	Slot        string
	LocalDate   time.Time // civil date of the send, UTC midnight
	TargetDate  time.Time // civil date of the good day, UTC midnight
	SpotID      uuid.UUID
	SpotName    string
	SpotTZ      string
	WindowStart time.Time
	WindowEnd   time.Time
	Score       int
}

type Stats struct {
	Candidates int
	Due        int
	Groups     int
	NoWindow   int
	Sent       int
	Skipped    int
	Failed     int
}
