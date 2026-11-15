package devices

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	Upsert(ctx context.Context, in RegisterInput) (uuid.UUID, error)

	Exists(ctx context.Context, id uuid.UUID) (bool, error)

	Delete(ctx context.Context, id uuid.UUID) (bool, error)
}
