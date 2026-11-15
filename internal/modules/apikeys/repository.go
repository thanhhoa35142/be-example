package apikeys

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	CreateKey(ctx context.Context, k ApiKey, keyHash []byte, grants []Grant) error

	FindByHash(ctx context.Context, keyHash []byte) (ApiKey, error)

	GrantsForKey(ctx context.Context, apiKeyID uuid.UUID) ([]Grant, error)

	TouchLastUsed(ctx context.Context, apiKeyID uuid.UUID) error

	ListKeys(ctx context.Context) ([]ApiKey, error)

	Revoke(ctx context.Context, id uuid.UUID) (bool, error)

	AddGrant(ctx context.Context, apiKeyID uuid.UUID, g Grant) error
	RemoveGrant(ctx context.Context, apiKeyID uuid.UUID, g Grant) (bool, error)
}

type GrantSpec struct {
	Effect   string `json:"effect"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}
