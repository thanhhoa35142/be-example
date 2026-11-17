package devices

import (
	"context"

	"github.com/google/uuid"
)

type UpsertResult struct {
	ID         uuid.UUID
	SecretHash string
}

// Toucher is implemented by repositories that record device activity
// (t_devices.last_seen_at). It is optional so that tests with a minimal fake
// repository keep working.
type Toucher interface {
	Touch(ctx context.Context, id uuid.UUID) error
}

type Repository interface {
	Upsert(ctx context.Context, in RegisterInput) (UpsertResult, error)

	// SecretHash returns the stored credential hash for a live device, or
	// pgx.ErrNoRows when the device does not exist / is deleted / has none.
	SecretHash(ctx context.Context, id uuid.UUID) (string, error)

	Exists(ctx context.Context, id uuid.UUID) (bool, error)

	Delete(ctx context.Context, id uuid.UUID) (bool, error)
}
