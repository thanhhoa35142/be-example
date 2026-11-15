package events

import (
	"encoding/json"
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
	MaxEventsPerDay = 5000
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Input struct {
	Name  string         `json:"name"`
	Props map[string]any `json:"props,omitempty"`
}

type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

func validate(in Input) error {
	if !nameRe.MatchString(in.Name) {
		return fmt.Errorf("%w: name must match [a-z][a-z0-9_]{0,63}", ErrInvalidEvent)
	}
	if len(in.Props) > 0 {
		b, err := json.Marshal(in.Props)
		if err != nil {
			return fmt.Errorf("%w: props not serializable", ErrInvalidEvent)
		}
		if len(b) > MaxPropsBytes {
			return fmt.Errorf("%w: props over %d bytes", ErrInvalidEvent, MaxPropsBytes)
		}
	}
	return nil
}
