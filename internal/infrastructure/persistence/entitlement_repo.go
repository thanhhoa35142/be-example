package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
)

type EntitlementRepo struct {
	q *Queries
}

func NewEntitlementRepo(q *Queries) *EntitlementRepo {
	return &EntitlementRepo{q: q}
}

func (r *EntitlementRepo) DeviceExists(ctx context.Context, deviceID string) (bool, error) {
	id, err := uuid.Parse(deviceID)
	if err != nil {
		return false, nil
	}
	return r.q.DeviceExistsForAlert(ctx, id)
}

func tsParam(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func (r *EntitlementRepo) UpsertSubscription(ctx context.Context, s entitlement.Subscription) error {
	return r.q.UpsertSubscription(ctx, UpsertSubscriptionParams{
		OriginalTransactionID: s.OriginalTransactionID,
		ProductID:             s.ProductID,
		Status:                s.Status,
		Environment:           s.Environment,
		Ownership:             s.Ownership,
		IsTrial:               s.IsTrial,
		AutoRenew:             s.AutoRenew,
		ExpiresAt:             tsParam(s.ExpiresAt),
		GraceExpiresAt:        tsParam(s.GraceExpiresAt),
		RevokedAt:             tsParam(s.RevokedAt),
		LastSignedDate:        s.LastSignedDate,
	})
}

func (r *EntitlementRepo) LinkDevice(ctx context.Context, deviceID, originalTransactionID string) error {
	id, err := uuid.Parse(deviceID)
	if err != nil {
		return entitlement.ErrDeviceNotFound
	}
	return r.q.LinkDeviceSubscription(ctx, LinkDeviceSubscriptionParams{
		DeviceID:              id,
		OriginalTransactionID: originalTransactionID,
	})
}

func (r *EntitlementRepo) ActiveSubscription(ctx context.Context, deviceID string) (*entitlement.Subscription, error) {
	id, err := uuid.Parse(deviceID)
	if err != nil {
		return nil, nil
	}
	row, err := r.q.ActiveSubscriptionForDevice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entitlement.Subscription{
		OriginalTransactionID: row.OriginalTransactionID,
		ProductID:             row.ProductID,
		Status:                row.Status,
		Environment:           row.Environment,
		Ownership:             row.Ownership,
		IsTrial:               row.IsTrial,
		AutoRenew:             row.AutoRenew,
		ExpiresAt:             timePtr(row.ExpiresAt),
		GraceExpiresAt:        timePtr(row.GraceExpiresAt),
		RevokedAt:             timePtr(row.RevokedAt),
		LastSignedDate:        row.LastSignedDate,
	}, nil
}

func (r *EntitlementRepo) RecordNotification(ctx context.Context, n entitlement.NotificationRecord) error {
	uuidBytes, err := uuid.Parse(n.UUID)
	if err != nil {
		return entitlement.ErrInvalidNotice
	}
	payload, err := json.Marshal(json.RawMessage(n.Payload))
	if err != nil {
		return err
	}
	return r.q.InsertAppleNotification(ctx, InsertAppleNotificationParams{
		NotificationUuid:      uuidBytes,
		Type:                  n.Type,
		Subtype:               textParam(&n.Subtype),
		OriginalTransactionID: textParam(&n.OriginalTransactionID),
		SignedDate:            n.SignedDate,
		Environment:           n.Environment,
		Payload:               payload,
	})
}

func (r *EntitlementRepo) SubscriptionLastSignedDate(ctx context.Context, originalTransactionID string) (time.Time, bool, error) {
	t, err := r.q.SubscriptionLastSignedDate(ctx, originalTransactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

func (r *EntitlementRepo) MarkNotificationProcessed(ctx context.Context, uuidStr string) error {
	return r.markNotification(ctx, uuidStr, func(id uuid.UUID) error {
		return r.q.MarkNotificationProcessed(ctx, id)
	})
}

func (r *EntitlementRepo) MarkNotificationIgnored(ctx context.Context, uuidStr string) error {
	return r.markNotification(ctx, uuidStr, func(id uuid.UUID) error {
		return r.q.MarkNotificationIgnored(ctx, id)
	})
}

func (r *EntitlementRepo) MarkNotificationFailed(ctx context.Context, uuidStr, lastError string) error {
	return r.markNotification(ctx, uuidStr, func(id uuid.UUID) error {
		return r.q.MarkNotificationFailed(ctx, MarkNotificationFailedParams{
			NotificationUuid: id,
			LastError:        pgtype.Text{String: lastError, Valid: lastError != ""},
		})
	})
}

func (r *EntitlementRepo) markNotification(ctx context.Context, uuidStr string, fn func(uuid.UUID) error) error {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return nil // unparseable uuid can't be in the table anyway
	}
	return fn(id)
}
