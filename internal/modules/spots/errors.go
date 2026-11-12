package spots

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound     = errors.New("spot not found")
	ErrInvalidBBox  = errors.New("invalid bbox")
	ErrBboxTooLarge = errors.New("bbox too large")
)

// MergedError is returned when a spot was merged into another during re-import.
// The API answers 301 with NewID per the architecture spec.
type MergedError struct {
	NewID uuid.UUID
}

func (e MergedError) Error() string {
	return "spot merged into " + e.NewID.String()
}
