// Package apikeys manages scoped machine credentials for agent/MCP clients.
// Keys are bearer secrets ("phk_..."); only their SHA-256 hash is stored.
// Authorization is grant-based over (resource, action) pairs with
// deny-wins evaluation — see policy.go.
package apikeys

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidKey    = errors.New("invalid api key")
	ErrKeyNotFound   = errors.New("api key not found")
	ErrKeyRevoked    = errors.New("api key revoked")
	ErrKeyExpired    = errors.New("api key expired")
	ErrForbidden     = errors.New("forbidden: insufficient scope")
	ErrInvalidGrant  = errors.New("invalid grant")
	ErrDuplicateName = errors.New("api key name already exists")
)

// ApiKey is the stored credential record. The raw secret is never persisted.
type ApiKey struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	KeyPrefix    string     `json:"keyPrefix"`
	RateLimitRPS int        `json:"rateLimitRps"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt"`
	RevokedAt    *time.Time `json:"revokedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Grant is one allow/deny rule over a (resource, action) pair.
// "*" in either field is a wildcard matching everything.
type Grant struct {
	Effect    string    `json:"effect"` // "allow" | "deny"
	Resource  string    `json:"resource"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// KeyGrant pairs a grant with its owning key id (list views).
type KeyGrant struct {
	Grant
	APIKeyID uuid.UUID `json:"apiKeyId"`
}

// KeyWithGrants is an ApiKey plus its grant list (admin list view).
type KeyWithGrants struct {
	ApiKey
	Grants []Grant `json:"grants"`
}

// Auth is the resolved identity+policy attached to an authenticated request.
type Auth struct {
	Key    ApiKey
	Grants []Grant
}
