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
	MaxSpots = 20

	DefaultSendDow  = int(time.Thursday)
	DefaultSendHour = 18

	MinHourScore   = 60
	MinWindowHours = 2
	MaxWindowHours = 4

	DayStartHour = 8
	DayEndHour   = 20

	Kind = "digest.weekend"
)

type Digest struct {
	DeviceID   uuid.UUID
	SpotIDs    []uuid.UUID
	Enabled    bool
	SendDow    int
	SendHour   int
	LastSentOn *time.Time
}

type Candidate struct {
	Digest
	DeviceTZ string
}

type SpotInfo struct {
	ID   uuid.UUID
	Name string
	Kind string
	TZ   string
}

type HourScore struct {
	Start time.Time
	Score int
}

type Window struct {
	Start time.Time
	End   time.Time
	Mean  float64
}

type Delivery struct {
	DeviceID    uuid.UUID
	SpotID      uuid.UUID
	SpotName    string
	SpotKind    string
	SpotTZ      string
	WindowStart time.Time
	WindowEnd   time.Time
	Score       int
}

type Stats struct {
	Candidates int
	Due        int
	Sent       int
	NoWindow   int
	Skipped    int
	Failed     int
}
