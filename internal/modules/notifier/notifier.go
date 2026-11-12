package notifier

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"gitlab.com/walkalike-group/pickhour/internal/modules/alerts"
)

// TokenProvider resolves a device's APNs token, environment and locale.
// alerts.Repository satisfies this interface.
type TokenProvider interface {
	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)
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
