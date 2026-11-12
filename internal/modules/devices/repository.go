package devices

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the persistence port for the devices module.
type Repository interface {
	// Upsert creates or refreshes a device; returns ErrConflict if the
	// APNs token is already bound to a different device ID.
	Upsert(ctx context.Context, in RegisterInput) (uuid.UUID, error)
	// Exists reports whether an active (non-deleted) device exists.
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// Delete soft-deletes a device (GDPR/CCPA unlink).
	Delete(ctx context.Context, id uuid.UUID) (bool, error)
}
