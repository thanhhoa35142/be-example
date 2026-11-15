package entitlement

import (
	"context"
	"time"
)

type Repository interface {
	DeviceExists(ctx context.Context, deviceID string) (bool, error)

	UpsertSubscription(ctx context.Context, s Subscription) (applied bool, err error)

	LinkDevice(ctx context.Context, deviceID, originalTransactionID string, maxDevices int) error

	ActiveSubscription(ctx context.Context, deviceID string) (*Subscription, error)

	RecordNotification(ctx context.Context, n NotificationRecord) error

	SubscriptionLastSignedDate(ctx context.Context, originalTransactionID string) (signedAt time.Time, ok bool, err error)

	MarkNotificationProcessed(ctx context.Context, uuid string) error
	MarkNotificationIgnored(ctx context.Context, uuid string) error
	MarkNotificationFailed(ctx context.Context, uuid string, lastError string) error
}

type NotificationRecord struct {
	UUID                  string
	Type                  string
	Subtype               string
	OriginalTransactionID string
	SignedDate            time.Time
	Environment           string
	Payload               []byte
}
