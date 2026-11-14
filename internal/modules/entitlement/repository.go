package entitlement

import (
	"context"
	"time"
)

// Repository is the persistence port for the entitlement module.
type Repository interface {
	// DeviceExists reports whether an active device exists.
	DeviceExists(ctx context.Context, deviceID string) (bool, error)
	// UpsertSubscription inserts or refreshes a subscription row. applied is
	// false when the write was dropped as stale (older last_signed_date) or
	// because it would un-revoke a revoked/refunded subscription.
	UpsertSubscription(ctx context.Context, s Subscription) (applied bool, err error)
	// LinkDevice attaches a subscription to a device, atomically enforcing
	// maxDevices active devices per transaction (ErrDeviceLimit). Linking an
	// already-linked pair succeeds without counting.
	LinkDevice(ctx context.Context, deviceID, originalTransactionID string, maxDevices int) error
	// ActiveSubscription returns the device's current entitled subscription.
	ActiveSubscription(ctx context.Context, deviceID string) (*Subscription, error)
	// RecordNotification stores an App Store Server Notification for audit.
	RecordNotification(ctx context.Context, n NotificationRecord) error
	// SubscriptionLastSignedDate returns the stored last_signed_date for a
	// subscription, or ok=false when none exists.
	SubscriptionLastSignedDate(ctx context.Context, originalTransactionID string) (signedAt time.Time, ok bool, err error)
	// MarkNotificationProcessed/Ignored/Failed drive the notification state
	// machine (received → processed|ignored|failed).
	MarkNotificationProcessed(ctx context.Context, uuid string) error
	MarkNotificationIgnored(ctx context.Context, uuid string) error
	MarkNotificationFailed(ctx context.Context, uuid string, lastError string) error
}

// NotificationRecord is the audit row for apple_notification.
type NotificationRecord struct {
	UUID                  string
	Type                  string
	Subtype               string
	OriginalTransactionID string
	SignedDate            time.Time
	Environment           string
	Payload               []byte
}
