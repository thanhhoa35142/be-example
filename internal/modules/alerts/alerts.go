package alerts

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

var (
	ErrNotFound      = errors.New("alert rule not found")
	ErrInvalidRule   = errors.New("invalid alert rule")
	ErrPlanLimit     = errors.New("plan limit reached")
	ErrDeviceInvalid = errors.New("device not found")
	ErrSpotNotFound  = errors.New("spot not found")

	ErrNoPushToken = errors.New("device has no push token")

	ErrPushTokenDead = errors.New("apns token unregistered")
)

const MaxDeliveryAttempts = 3

type Rule struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	SpotID     *uuid.UUID
	H3Cell     *uint64
	RadiusM    *int
	MinScore   int
	DaysMask   int
	StartLocal time.Duration
	EndLocal   time.Duration
	TZ         string
	Enabled    bool
}

func (r Rule) AreaCenter() (lat, lon float64, ok bool) {
	if r.H3Cell == nil {
		return 0, 0, false
	}
	lat, lon, err := geo.CellCenter(int64(*r.H3Cell))
	if err != nil {
		return 0, 0, false
	}
	return lat, lon, true
}

type LatLon struct {
	Lat float64
	Lon float64
}

type RuleInput struct {
	SpotID     *uuid.UUID
	H3Cell     *uint64
	Area       *LatLon
	RadiusM    *int
	MinScore   int
	DaysMask   int
	StartLocal string
	EndLocal   string
	TZ         string
	Enabled    *bool
}

type Delivery struct {
	RuleID      uuid.UUID
	DeviceID    uuid.UUID
	WindowStart time.Time
	SpotID      uuid.UUID
	SpotName    string
	Score       int
}

type Stats struct {
	Rules     int
	Evaluated int
	Fired     int
	Sent      int
	Failed    int
}

var Limits = map[string]int{
	"free": 1,
	// Plus is generous but finite — unlimited rules let one device make the
	// evaluator loop over unbounded per-rule queries every run.
	"plus": 20,
}

func parseHHMM(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%w: time must be HH:MM", ErrInvalidRule)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

func validTZ(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

type parsedRule struct {
	RuleInput
	start, end time.Duration
}

func normalize(in RuleInput) (RuleInput, error) {
	n, err := validate(in)
	return n.RuleInput, err
}

func validate(in RuleInput) (parsedRule, error) {
	out, err := normalizeShape(in)
	if err != nil {
		return parsedRule{RuleInput: out}, err
	}
	start, err := parseHHMM(out.StartLocal)
	if err != nil {
		return parsedRule{RuleInput: out}, fmt.Errorf("%w: startLocal must be HH:MM", ErrInvalidRule)
	}
	end, err := parseHHMM(out.EndLocal)
	if err != nil {
		return parsedRule{RuleInput: out}, fmt.Errorf("%w: endLocal must be HH:MM", ErrInvalidRule)
	}
	if start == end {

		return parsedRule{RuleInput: out}, fmt.Errorf("%w: startLocal and endLocal must differ", ErrInvalidRule)
	}
	return parsedRule{RuleInput: out, start: start, end: end}, nil
}

func normalizeShape(in RuleInput) (RuleInput, error) {
	shapes := 0
	if in.SpotID != nil {
		shapes++
	}
	if in.H3Cell != nil {
		shapes++
	}
	if in.Area != nil {
		shapes++
	}
	if shapes != 1 {
		return in, fmt.Errorf("%w: exactly one of spotId, h3Cell or area required", ErrInvalidRule)
	}
	if in.Area != nil {
		cell, err := AreaCell(*in.Area)
		if err != nil {
			return in, err
		}
		in.H3Cell = &cell
		in.Area = nil
	}
	if in.H3Cell != nil {
		// The cell is stored as bigint — values above MaxInt64 would wrap
		// negative and silently produce a rule that never fires.
		if *in.H3Cell > math.MaxInt64 {
			return in, fmt.Errorf("%w: h3Cell out of range", ErrInvalidRule)
		}
		if in.RadiusM == nil || *in.RadiusM <= 0 || *in.RadiusM > 50000 {
			return in, fmt.Errorf("%w: radiusM must be 1..50000 for area rules", ErrInvalidRule)
		}
	}
	if in.SpotID != nil && in.RadiusM != nil {
		return in, fmt.Errorf("%w: radiusM only applies to area rules", ErrInvalidRule)
	}
	if in.MinScore < 0 || in.MinScore > 100 {
		return in, fmt.Errorf("%w: minScore must be 0..100", ErrInvalidRule)
	}
	if in.DaysMask <= 0 || in.DaysMask > 127 {
		return in, fmt.Errorf("%w: daysMask must select 1..7 weekdays", ErrInvalidRule)
	}
	if !validTZ(in.TZ) {
		return in, fmt.Errorf("%w: tz must be an IANA time zone", ErrInvalidRule)
	}
	return in, nil
}

func round2(v float64) float64 {
	r := math.Round(v*100) / 100
	if r == 0 {
		return 0
	}
	return r
}

func AreaCell(p LatLon) (uint64, error) {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lon, 0) ||
		p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return 0, fmt.Errorf("%w: area lat/lon out of range", ErrInvalidRule)
	}
	id, err := geo.CellIDFromLatLng(round2(p.Lat), round2(p.Lon))
	if err != nil {
		return 0, fmt.Errorf("%w: area not convertible to a cell", ErrInvalidRule)
	}
	return uint64(id), nil
}
