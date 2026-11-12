package apikeys

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the persistence port for API keys and their grants.
type Repository interface {
	// CreateKey inserts the key row and its grants atomically.
	CreateKey(ctx context.Context, k ApiKey, keyHash []byte, grants []Grant) error

	// FindByHash returns the key (without grants) for a SHA-256 hash.
	FindByHash(ctx context.Context, keyHash []byte) (ApiKey, error)

	// GrantsForKey returns all grants of a key.
	GrantsForKey(ctx context.Context, apiKeyID uuid.UUID) ([]Grant, error)

	// TouchLastUsed stamps last_used_at; called off the request path.
	TouchLastUsed(ctx context.Context, apiKeyID uuid.UUID) error

	ListKeys(ctx context.Context) ([]ApiKey, error)

	// Revoke marks the key revoked; returns false if already revoked/missing.
	Revoke(ctx context.Context, id uuid.UUID) (bool, error)

	AddGrant(ctx context.Context, apiKeyID uuid.UUID, g Grant) error
	RemoveGrant(ctx context.Context, apiKeyID uuid.UUID, g Grant) (bool, error)
}

// GrantSpec is grant input at creation time (no timestamps).
type GrantSpec struct {
	Effect   string `json:"effect"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}
