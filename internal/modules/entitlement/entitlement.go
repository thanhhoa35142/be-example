package entitlement

import (
	"errors"
	"time"
)

var (
	ErrDeviceNotFound = errors.New("device not found")
	ErrInvalidJWS     = errors.New("invalid signed transaction")
	ErrInvalidNotice  = errors.New("invalid app store notification")

	ErrNotConfigured = errors.New("app store verification not configured")

	ErrDeviceLimit = errors.New("subscription linked to too many devices")

	ErrAccountTokenMismatch = errors.New("transaction belongs to another device")
)

const MaxDevicesPerSubscription = 5

type Config struct {
	BundleID string

	AllowSandbox bool
}

type Plan string

const (
	PlanFree Plan = "free"
	PlanPlus Plan = "plus"
)

type Subscription struct {
	OriginalTransactionID string
	ProductID             string
	Status                string
	Environment           string
	Ownership             string
	IsTrial               bool
	AutoRenew             bool
	ExpiresAt             *time.Time
	GraceExpiresAt        *time.Time
	RevokedAt             *time.Time
	LastSignedDate        time.Time
}

type Entitlement struct {
	Plan         Plan
	Subscription *Subscription
}
