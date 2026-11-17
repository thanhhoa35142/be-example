package notifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

type TokenProvider interface {
	DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (token, env, locale string, ok bool, err error)
}

type Unregisterer interface {
	MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error
}

// PushRecorder is optionally implemented by the TokenProvider: it records a
// successful APNs send into t_push_sends, the cross-kind frequency-cap ledger
// the good-day nudge reads. Recording is best-effort and never blocks a push.
type PushRecorder interface {
	RecordPushSend(ctx context.Context, deviceID uuid.UUID, kind string, sentAt time.Time) error
}

const alertKind = "alert.window"

type Sender struct {
	logger *slog.Logger
	tokens TokenProvider
	client *Client
}

func NewSender(logger *slog.Logger, tokens TokenProvider, client *Client) *Sender {
	return &Sender{logger: logger, tokens: tokens, client: client}
}

func (s *Sender) Send(ctx context.Context, d alerts.Delivery) error {
	token, env, _, ok, err := s.tokens.DeviceAPNsToken(ctx, d.DeviceID)
	if err != nil {
		return err
	}
	if !ok {
		return alerts.ErrNoPushToken
	}

	payload := buildPayload(d)
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
	s.recordSend(ctx, d.DeviceID, alertKind)
	s.logger.InfoContext(ctx, "alert pushed",
		"rule", d.RuleID, "device", d.DeviceID, "status", status)
	return nil
}

// recordSend notes a delivered push for the frequency cap. A failure is
// logged and swallowed: the push already left, and the cap only gates nudges.
func (s *Sender) recordSend(ctx context.Context, deviceID uuid.UUID, kind string) {
	r, ok := s.tokens.(PushRecorder)
	if !ok {
		return
	}
	if err := r.RecordPushSend(ctx, deviceID, kind, time.Now().UTC()); err != nil {
		s.logger.ErrorContext(ctx, "push send record failed", "device", deviceID, "kind", kind, "error", err)
	}
}
