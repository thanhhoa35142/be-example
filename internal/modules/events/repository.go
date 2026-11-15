package events

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	InsertBatch(ctx context.Context, deviceID uuid.UUID, payload []byte) error

	CountSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int64, error)

	DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error)
}
