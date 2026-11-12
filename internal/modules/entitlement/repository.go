package entitlement

import (
	"context"
	"time"
)

// Repository is the persistence port for the entitlement module.
type Repository interface {
	// DeviceExists reports whether an active device exists.
	DeviceExists(ctx context.Context, deviceID string) (bool, error)
	// UpsertSubscription inserts or refreshes a subscription row.
	UpsertSubscription(ctx context.Context, s Subscription) error
	// LinkDevice attaches a subscription to a device.
	LinkDevice(ctx context.Context, deviceID, originalTransactionID string) error
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
