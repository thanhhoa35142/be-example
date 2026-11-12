package notifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

// TokenProvider resolves a device's APNs token, environment and locale.
// alerts.Repository satisfies this interface.
type TokenProvider interface {
	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)
}

// Unregisterer clears a device's push credentials. Implemented by
// alerts.Repository; asserted optionally so test fakes keep working.
type Unregisterer interface {
	MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error
}

// Sender delivers fired alerts via APNs. When client is nil (no APNS_*
// credentials configured) it logs the exact payload it would have sent —
// dev mode.
type Sender struct {
	logger *slog.Logger
	tokens TokenProvider
	client *Client
}

func NewSender(logger *slog.Logger, tokens TokenProvider, client *Client) *Sender {
	return &Sender{logger: logger, tokens: tokens, client: client}
}

func (s *Sender) Send(ctx context.Context, d alerts.Delivery) error {
	token, env, locale, ok, err := s.tokens.DeviceAPNsToken(ctx, d.DeviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}

	payload := buildPayload(d, locale)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if s.client == nil {
		s.logger.InfoContext(ctx, "alert push (APNs not configured)",
			"rule", d.RuleID, "device", d.DeviceID,
			"spot", d.SpotName, "score", d.Score,
			"payload", string(body))
		return nil
	}

	status, err := s.client.Send(ctx, token, env, d.RuleID.String(), body)
	if status == http.StatusGone {
		// APNs 410: the token is permanently dead. Clear the device's push
		// credentials so future evaluations skip it, and signal the caller
		// to dead-letter the delivery instead of retrying.
		if u, ok := s.tokens.(Unregisterer); ok {
			if uerr := u.MarkDeviceUnregistered(ctx, d.DeviceID); uerr != nil {
				s.logger.ErrorContext(ctx, "device unregister failed",
					"device", d.DeviceID, "error", uerr)
			}
		}
		s.logger.InfoContext(ctx, "apns token unregistered (410)",
			"rule", d.RuleID, "device", d.DeviceID)
		return alerts.ErrPushTokenDead
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "apns send failed",
			"rule", d.RuleID, "device", d.DeviceID,
			"status", status, "error", err)
		return err
	}
	s.logger.InfoContext(ctx, "alert pushed",
		"rule", d.RuleID, "device", d.DeviceID, "status", status)
	return nil
}
