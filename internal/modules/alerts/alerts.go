// Package alerts owns alert rules ("good window" notifications) and
// delivery de-duplication via alert_delivery. Alert areas are stored
// coarsely (H3 cell or radius) for privacy; Free is limited to one rule.
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
	ErrPlanLimit     = errors.New("plan limit reached") // Free: 1 alert area
	ErrDeviceInvalid = errors.New("device not found")
	ErrSpotNotFound  = errors.New("spot not found")
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

// AreaCenter returns the center of the rule's H3 cell (area rules only).
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

// LatLon is a coarse client-supplied point. It is never stored: normalize
// rounds it to 2 decimals (~1 km) and converts it to an H3 cell.
type LatLon struct {
	Lat float64
	Lon float64
}

// RuleInput is the create/update payload. Exactly one of SpotID, H3Cell or
// Area must be set.
type RuleInput struct {
	SpotID     *uuid.UUID
	H3Cell     *uint64
	Area       *LatLon
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

// validTZ accepts only real IANA zone names. time.LoadLocation treats ""
// as UTC and "Local" as the server's zone, neither of which is a client choice.
func validTZ(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// parsedRule is a normalized RuleInput plus its parsed local window.
type parsedRule struct {
	RuleInput
	start, end time.Duration
}

// normalize validates and fills defaults for a RuleInput.
func normalize(in RuleInput) (RuleInput, error) {
	n, err := validate(in)
	return n.RuleInput, err
}

// validate is normalize plus the parsed start/end durations.
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
		// Overnight windows (start > end) are valid; a zero-length one is not.
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
		in.Area = nil // raw coordinates are dropped here, never stored or logged
	}
	if in.H3Cell != nil && (in.RadiusM == nil || *in.RadiusM <= 0 || *in.RadiusM > 50000) {
		return in, fmt.Errorf("%w: radiusM must be 1..50000 for area rules", ErrInvalidRule)
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

// round2 rounds to 2 decimals (~1.1 km of latitude).
func round2(v float64) float64 {
	r := math.Round(v*100) / 100
	if r == 0 {
		return 0 // avoid -0
	}
	return r
}

// AreaCell validates a coarse point, rounds it to 2 decimals and converts it
// to an H3 cell at geo.H3Resolution. The raw value is not retained.
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
