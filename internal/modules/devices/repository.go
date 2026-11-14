package devices

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the persistence port for the devices module.
type Repository interface {
	// Upsert creates or refreshes a device. An APNs token held by another
	// live device is taken over (the previous holder is demoted to
	// push_status 'unregistered'); ErrConflict only signals a lost race.
	Upsert(ctx context.Context, in RegisterInput) (uuid.UUID, error)
	// Exists reports whether an active (non-deleted) device exists.
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// Delete soft-deletes a device (GDPR/CCPA unlink).
	Delete(ctx context.Context, id uuid.UUID) (bool, error)
}
