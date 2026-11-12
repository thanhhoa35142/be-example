package events

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence port for the events module.
type Repository interface {
	// InsertBatch writes events for one device; payload is a JSON array of
	// {"name": ..., "props": {...}} objects.
	InsertBatch(ctx context.Context, deviceID uuid.UUID, payload []byte) error
	// CountSince returns events recorded for a device after `since`.
	CountSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int64, error)
	// DeviceExists reports whether an active device exists.
	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)
}
