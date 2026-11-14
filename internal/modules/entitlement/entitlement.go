// Package entitlement verifies StoreKit 2 transactions and App Store Server
// Notifications (JWS signature + x5c chain to Apple Root CA G3, bundle id,
// environment), maps devices to subscriptions, and resolves the effective plan
// (Free/Plus). Verification is fail-closed: without a verifier and a bundle id
// nothing is accepted.
package entitlement

import (
	"errors"
	"time"
)

var (
	ErrDeviceNotFound = errors.New("device not found")
	ErrInvalidJWS     = errors.New("invalid signed transaction")
	ErrInvalidNotice  = errors.New("invalid app store notification")
	// ErrNotConfigured means the App Store verifier or bundle id is not set;
	// every signed payload is refused (fail closed).
	ErrNotConfigured = errors.New("app store verification not configured")
	// ErrDeviceLimit means the subscription is already linked to the maximum
	// number of active devices.
	ErrDeviceLimit = errors.New("subscription linked to too many devices")
	// ErrAccountTokenMismatch means the transaction's appAccountToken names a
	// different device than the one linking it.
	ErrAccountTokenMismatch = errors.New("transaction belongs to another device")
)

// MaxDevicesPerSubscription caps active (non-deleted) devices that can share
// one original_transaction_id. Covers one user's phones/iPads/reinstalls but
// stops a purchased transaction being handed to unlimited devices.
const MaxDevicesPerSubscription = 5

// Config is the App Store verification policy.
type Config struct {
	// BundleID the app is published under; payloads for any other bundle are
	// rejected. Empty = verification disabled (everything refused).
	BundleID string
	// AllowSandbox accepts Sandbox-environment payloads in addition to
	// Production. App Review exercises purchases in Sandbox against the
	// production server, so this defaults to true; turn off to harden once the
	// app is live (Sandbox subscriptions are free to create).
	AllowSandbox bool
}

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
