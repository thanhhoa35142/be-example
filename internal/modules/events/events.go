// Package events ingests privacy-safe product analytics events from the app
// (batch POST). Events carry no location; props are size-capped. Rows cascade
// on device delete for GDPR.
package events

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrInvalidEvent  = errors.New("invalid event")
	ErrRateLimited   = errors.New("too many events")
	ErrDeviceInvalid = errors.New("device not found")
)

const (
	MaxBatchSize    = 100
	MaxPropsBytes   = 4096
	MaxEventsPerDay = 5000 // per device, abuse guard
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Input is one event in a batch.
type Input struct {
	Name  string         `json:"name"`
	Props map[string]any `json:"props,omitempty"`
}

// DayCount is a per-day aggregate for admin summaries.
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

// NameCount is a per-event-name aggregate.
type NameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

func validate(in Input) error {
	if !nameRe.MatchString(in.Name) {
		return fmt.Errorf("%w: name must match [a-z][a-z0-9_]{0,63}", ErrInvalidEvent)
	}
	return nil
}
