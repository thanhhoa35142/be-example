package entitlement

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo     Repository
	verifier PayloadVerifier
	cfg      Config
	now      func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithVerifier installs the JWS verifier and policy. Without it the service
// still resolves plans but refuses every signed payload (ErrNotConfigured).
func WithVerifier(v PayloadVerifier, cfg Config) Option {
	return func(s *Service) { s.verifier, s.cfg = v, cfg }
}

func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// LinkTransaction verifies a StoreKit signed transaction (signature, Apple
// chain, bundle id, environment), upserts the subscription, and links it to
// the device. Returns ErrDeviceNotFound when the device is unknown,
// ErrAccountTokenMismatch when the transaction's appAccountToken names another
// device, and ErrDeviceLimit when the transaction is already on
// MaxDevicesPerSubscription active devices.
func (s *Service) LinkTransaction(ctx context.Context, deviceID, signedTransaction string) (*Entitlement, error) {
	sub, accountToken, err := s.decodeTransaction(signedTransaction)
	if err != nil {
		return nil, err
	}
	// appAccountToken is optional; when the app sets it to the device id and
	// it parses as a UUID it must match the device linking the transaction.
	if tok, perr := uuid.Parse(accountToken); perr == nil {
		if dev, derr := uuid.Parse(deviceID); derr != nil || tok != dev {
			return nil, ErrAccountTokenMismatch
		}
	}
	exists, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrDeviceNotFound
	}
	// A stale transaction (older signedDate) or an attempt to un-revoke is
	// dropped by the upsert; the plan below then reflects the stored state.
	if _, err := s.repo.UpsertSubscription(ctx, *sub); err != nil {
		return nil, err
	}
	if err := s.repo.LinkDevice(ctx, deviceID, sub.OriginalTransactionID, MaxDevicesPerSubscription); err != nil {
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
	n, sub, rawPayload, err := s.decodeNotification(signedPayload)
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
		Environment:           n.Data.Environment,
		Payload:               rawPayload,
	}); err != nil {
		return err
	}
	if sub == nil {
		return s.repo.MarkNotificationProcessed(ctx, n.NotificationUUID)
	}
	// Out-of-order guard: ignore notifications whose signed_date is not newer
	// than what the subscription already applied.
	if lastSigned, ok, err := s.repo.SubscriptionLastSignedDate(ctx, otid); err != nil {
		return s.markFailed(ctx, n.NotificationUUID, err)
	} else if ok && !lastSigned.Before(signedAt) {
		return s.repo.MarkNotificationIgnored(ctx, n.NotificationUUID)
	}
	applied, err := s.repo.UpsertSubscription(ctx, *sub)
	if err != nil {
		return s.markFailed(ctx, n.NotificationUUID, err)
	}
	if !applied {
		// Stale by the transaction's own signedDate, or it would un-revoke.
		return s.repo.MarkNotificationIgnored(ctx, n.NotificationUUID)
	}
	slog.InfoContext(ctx, "app store notification processed",
		"type", n.NotificationType, "subtype", n.Subtype,
		"original_transaction_id", otid)
	return s.repo.MarkNotificationProcessed(ctx, n.NotificationUUID)
}

func (s *Service) markFailed(ctx context.Context, uuid string, cause error) error {
	if err := s.repo.MarkNotificationFailed(ctx, uuid, cause.Error()); err != nil {
		slog.ErrorContext(ctx, "notification fail-mark failed", "uuid", uuid, "error", err)
	}
	return cause
}
