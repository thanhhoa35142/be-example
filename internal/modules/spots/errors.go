package spots

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound     = errors.New("spot not found")
	ErrInvalidBBox  = errors.New("invalid bbox")
	ErrBboxTooLarge = errors.New("bbox too large")

	ErrInvalidQuery = errors.New("invalid query")
)

type MergedError struct {
	NewID uuid.UUID
}

func (e MergedError) Error() string {
	return "spot merged into " + e.NewID.String()
}
