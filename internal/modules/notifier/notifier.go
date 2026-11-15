package notifier

import (
	"context"
	"encoding/json"
	"fmt"
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
	body, err := json.Marshal(buildPayload(d))
	if err != nil {
		return err
	}
	return s.push(ctx, d.DeviceID, d.RuleID.String(), kindAlertWindow, body)
}

// push delivers one encoded payload to a device. Errors follow the alerts
// contract: ErrNoPushToken when the device has no active token,
// ErrPushTokenDead on APNs 410 (the device is marked unregistered first).
func (s *Sender) push(ctx context.Context, deviceID uuid.UUID, collapseID, kind string, body []byte) error {
	token, env, _, ok, err := s.tokens.DeviceAPNsToken(ctx, deviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}
	if s.client == nil {
		s.logger.InfoContext(ctx, "push (APNs not configured)",
			"kind", kind, "device", deviceID, "payload", string(body))
		return nil
	}

	status, err := s.client.Send(ctx, token, env, collapseID, body)
	if status == http.StatusGone {
		if u, ok := s.tokens.(Unregisterer); ok {
			if uerr := u.MarkDeviceUnregistered(ctx, deviceID); uerr != nil {
				s.logger.ErrorContext(ctx, "device unregister failed",
					"device", deviceID, "error", uerr)
			}
		}
		s.logger.InfoContext(ctx, "apns token unregistered (410)",
			"kind", kind, "device", deviceID)
		return alerts.ErrPushTokenDead
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "apns send failed",
			"kind", kind, "device", deviceID, "status", status, "error", err)
		return fmt.Errorf("apns status %d: %w", status, err)
	}
	s.logger.InfoContext(ctx, "pushed", "kind", kind, "device", deviceID, "status", status)
	return nil
}
