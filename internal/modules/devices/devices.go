package devices

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("device not found")
	ErrConflict = errors.New("conflict") // e.g. APNs token bound to another device
)

// RegisterInput is the payload for POST /v1/devices.
// ID is generated client-side once and kept in the Keychain.
type RegisterInput struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string // "sandbox" | "production"
	Locale     *string
	TZ         *string
	AppVersion *string
	Prefs      map[string]any
}

// Device is the stored record (no location fields by design).
type Device struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string
	Locale     *string
	TZ         *string
	AppVersion *string
	LastSeenAt time.Time
}
