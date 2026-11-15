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

type Grant struct {
	Effect    string    `json:"effect"`
	Resource  string    `json:"resource"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type KeyGrant struct {
	Grant
	APIKeyID uuid.UUID `json:"apiKeyId"`
}

type KeyWithGrants struct {
	ApiKey
	Grants []Grant `json:"grants"`
}

type Auth struct {
	Key    ApiKey
	Grants []Grant
}
