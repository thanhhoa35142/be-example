// Package entitlement verifies StoreKit 2 transactions and App Store Server
// Notifications, maps devices to subscriptions, and resolves the effective
// plan (Free/Plus). Signature verification against Apple's certificate chain
// is a production stub; the JWS payload is decoded for development.
package entitlement

import (
	"errors"
	"time"
)

var (
	ErrDeviceNotFound = errors.New("device not found")
	ErrInvalidJWS     = errors.New("invalid signed transaction")
	ErrInvalidNotice  = errors.New("invalid app store notification")
)

// Plan is the billing tier.
type Plan string

const (
	PlanFree Plan = "free"
	PlanPlus Plan = "plus"
)

// Subscription mirrors the subscription table.
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

// Entitlement is the resolved state for a device.
type Entitlement struct {
	Plan         Plan
	Subscription *Subscription // nil when on Free
}
