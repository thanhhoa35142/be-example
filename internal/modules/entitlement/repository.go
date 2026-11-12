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
	// MarkNotificationProcessed stamps processed_at.
	MarkNotificationProcessed(ctx context.Context, uuid string) error
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
