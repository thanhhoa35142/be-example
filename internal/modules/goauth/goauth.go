// Package goauth links an admin console account to a Google identity so the
// console can call Search Console and GA4 on the admin's behalf. The OAuth
// refresh token is sealed with the same AES-256-GCM key family as TOTP
// secrets (MFA_SECRET_KEYS) before it reaches the database.
package goauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound       = errors.New("google account not linked")
	ErrNotConfigured  = errors.New("google oauth not configured")
	ErrStateInvalid   = errors.New("invalid oauth state")
	ErrStateExpired   = errors.New("oauth state expired")
	ErrNoRefreshToken = errors.New("google did not return a refresh token")
	ErrExchangeFailed = errors.New("google token exchange failed")
	ErrUserInfoFailed = errors.New("google userinfo request failed")
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c Config) Ready() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// Token is the stored link. RefreshToken is the sealed (encrypted) value;
// callers must never see plaintext outside the service.
type Token struct {
	AdminUserID  uuid.UUID
	GoogleSub    string
	GoogleEmail  string
	RefreshToken []byte
	Scopes       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Status is the safe public view — no token material.
type Status struct {
	Email     string    `json:"email"`
	Scopes    string    `json:"scopes"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Repository interface {
	Upsert(ctx context.Context, t Token) error
	Get(ctx context.Context, userID uuid.UUID) (*Token, error)
	Delete(ctx context.Context, userID uuid.UUID) error
}
