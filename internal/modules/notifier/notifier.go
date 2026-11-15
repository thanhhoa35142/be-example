package notifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

type TokenProvider interface {
	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)
}

type Unregisterer interface {
	MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error
}

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
