package entitlement

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// LinkTransaction verifies (decodes) a StoreKit signed transaction, upserts
// the subscription, and links it to the device. Returns ErrDeviceNotFound
// when the device is unknown.
func (s *Service) LinkTransaction(ctx context.Context, deviceID, signedTransaction string) (*Entitlement, error) {
	exists, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrDeviceNotFound
	}
	sub, err := decodeTransaction(signedTransaction)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpsertSubscription(ctx, *sub); err != nil {
		return nil, err
	}
	if err := s.repo.LinkDevice(ctx, deviceID, sub.OriginalTransactionID); err != nil {
		return nil, err
	}
	return s.Entitlement(ctx, deviceID)
}

// Plan implements alerts.PlanResolver.
func (s *Service) Plan(ctx context.Context, deviceID uuid.UUID) (string, error) {
	e, err := s.Entitlement(ctx, deviceID.String())
	if err != nil {
		return "", err
	}
	return string(e.Plan), nil
}

// Entitlement resolves the effective plan for a device.
func (s *Service) Entitlement(ctx context.Context, deviceID string) (*Entitlement, error) {
	sub, err := s.repo.ActiveSubscription(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return &Entitlement{Plan: PlanFree}, nil
	}
	return &Entitlement{Plan: PlanPlus, Subscription: sub}, nil
}

// ProcessNotification handles an App Store Server Notification V2: records it
// for audit and upserts the embedded transaction when present.
func (s *Service) ProcessNotification(ctx context.Context, signedPayload string) error {
	n, sub, rawPayload, err := decodeNotification(signedPayload)
	if err != nil {
		return err
	}
	otid := ""
	if sub != nil {
		otid = sub.OriginalTransactionID
	}
	signedAt := time.UnixMilli(n.SignedDate).UTC()
	if err := s.repo.RecordNotification(ctx, NotificationRecord{
		UUID:                  n.NotificationUUID,
		Type:                  n.NotificationType,
		Subtype:               n.Subtype,
		OriginalTransactionID: otid,
		SignedDate:            signedAt,
		Environment:           n.Environment,
		Payload:               rawPayload,
	}); err != nil {
		return err
	}
	if sub == nil {
		return s.repo.MarkNotificationProcessed(ctx, n.NotificationUUID)
	}
	if err := s.repo.UpsertSubscription(ctx, *sub); err != nil {
		return err
	}
	slog.InfoContext(ctx, "app store notification processed",
		"type", n.NotificationType, "subtype", n.Subtype,
		"original_transaction_id", otid)
	return s.repo.MarkNotificationProcessed(ctx, n.NotificationUUID)
}
