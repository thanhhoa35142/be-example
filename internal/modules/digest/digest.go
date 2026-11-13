package digest

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("digest not found")
	ErrInvalid       = errors.New("invalid digest")
	ErrPlanLimit     = errors.New("digest requires the plus plan")
	ErrDeviceInvalid = errors.New("device not found")
)

const (
	// MaxSpots caps spotIds per digest.
	MaxSpots = 20
	// DefaultSendDow / DefaultSendHour: Thursday 18:00 device-local.
	DefaultSendDow  = int(time.Thursday)
	DefaultSendHour = 18
	// MinHourScore / MinWindowHours define a qualifying picnic window.
	MinHourScore   = 60
	MinWindowHours = 2
	MaxWindowHours = 4 // longest window we advertise (ties prefer longer up to this)
	// DayStartHour/DayEndHour bound the window search in spot-local time.
	DayStartHour = 8
	DayEndHour   = 20
	// Kind is the APNs routing discriminator.
	Kind = "digest.weekend"
)

// Digest is a t_weekly_digests row.
type Digest struct {
	DeviceID   uuid.UUID
	SpotIDs    []uuid.UUID
	Enabled    bool
	SendDow    int // time.Weekday, 0=Sunday
	SendHour   int // device-local hour
	LastSentOn *time.Time
}

// Candidate is a Digest joined with its device's timezone ("" = unknown).
type Candidate struct {
	Digest
	DeviceTZ string
}

// SpotInfo is the spot data the digest needs.
type SpotInfo struct {
	ID   uuid.UUID
	Name string
	TZ   string
}

// HourScore is the score of the hour starting at Start.
type HourScore struct {
	Start time.Time
	Score int
}

// Window is a contiguous run of qualifying hours.
type Window struct {
	Start time.Time
	End   time.Time // exclusive
	Mean  float64
}

// Delivery is one digest push ready to send.
type Delivery struct {
	DeviceID    uuid.UUID
	SpotID      uuid.UUID
	SpotName    string
	SpotTZ      string
	WindowStart time.Time // UTC
	WindowEnd   time.Time // UTC
	Score       int       // rounded mean
}

// Stats summarizes a RunWeekly pass.
type Stats struct {
	Candidates int
	Due        int
	Sent       int
	NoWindow   int
	Skipped    int // not plus / no push token / already claimed
	Failed     int
}
