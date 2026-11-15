package entitlement

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type transactionPayload struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	ProductID             string `json:"productId"`
	BundleID              string `json:"bundleId"`
	Type                  string `json:"type"`
	ExpiresDate           *int64 `json:"expiresDate"`
	SignedDate            *int64 `json:"signedDate"`
	Environment           string `json:"environment"`
	OfferType             *int   `json:"offerType"`
	RevocationDate        *int64 `json:"revocationDate"`
	RevocationReason      *int   `json:"revocationReason"`
	AppAccountToken       string `json:"appAccountToken"`

	OwnershipType string `json:"ownershipType"`
}

type notificationPayload struct {
	NotificationType string `json:"notificationType"`
	Subtype          string `json:"subtype"`
	NotificationUUID string `json:"notificationUUID"`
	Data             struct {
		Environment           string `json:"environment"`
		BundleID              string `json:"bundleId"`
		SignedTransactionInfo string `json:"signedTransactionInfo"`
	} `json:"data"`
	Summary struct {
		Environment string `json:"environment"`
		BundleID    string `json:"bundleId"`
	} `json:"summary"`
	SignedDate int64 `json:"signedDate"`
}

func (n notificationPayload) bundleID() string {
	if n.Data.BundleID != "" {
		return n.Data.BundleID
	}
	return n.Summary.BundleID
}

func (n notificationPayload) environment() string {
	if n.Data.Environment != "" {
		return n.Data.Environment
	}
	return n.Summary.Environment
}

func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

func (c Config) checkEnvironment(env string) (string, error) {
	switch e := strings.ToLower(env); e {
	case "production":
		return e, nil
	case "sandbox":
		if c.AllowSandbox {
			return e, nil
		}
		return "", fmt.Errorf("%w: sandbox environment not accepted", ErrInvalidJWS)
	default:
		return "", fmt.Errorf("%w: unsupported environment", ErrInvalidJWS)
	}
}

func (s *Service) decodeTransaction(jws string) (*Subscription, string, error) {
	if s.verifier == nil || s.cfg.BundleID == "" {
		return nil, "", ErrNotConfigured
	}
	raw, err := s.verifier.VerifyPayload(jws)
	if err != nil {
		return nil, "", err
	}
	var p transactionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidJWS, err)
	}
	if p.OriginalTransactionID == "" || p.ProductID == "" {
		return nil, "", fmt.Errorf("%w: missing required fields", ErrInvalidJWS)
	}
	if p.SignedDate == nil {
		return nil, "", fmt.Errorf("%w: missing signedDate", ErrInvalidJWS)
	}
	if p.BundleID != s.cfg.BundleID {
		return nil, "", fmt.Errorf("%w: bundle id mismatch", ErrInvalidJWS)
	}
	if len(s.cfg.ProductIDs) > 0 && !s.cfg.ProductIDs[p.ProductID] {
		return nil, "", fmt.Errorf("%w: product not allowed", ErrInvalidJWS)
	}
	// A subscription transaction must carry expiresDate — without it the row
	// would be stored with expires_at NULL and read as active forever.
	if strings.Contains(p.Type, "Subscription") && p.ExpiresDate == nil {
		return nil, "", fmt.Errorf("%w: subscription without expiresDate", ErrInvalidJWS)
	}
	env, err := s.cfg.checkEnvironment(p.Environment)
	if err != nil {
		return nil, "", err
	}

	status := "active"
	switch {
	case p.RevocationDate != nil:
		status = "revoked"
	case p.ExpiresDate != nil && time.UnixMilli(*p.ExpiresDate).Before(s.now()):
		status = "expired"
	}

	ownership := "purchased"
	if strings.EqualFold(p.OwnershipType, "FAMILY_SHARED") {
		ownership = "family_shared"
	}

	return &Subscription{
		OriginalTransactionID: p.OriginalTransactionID,
		ProductID:             p.ProductID,
		Status:                status,
		Environment:           env,
		Ownership:             ownership,
		IsTrial:               p.OfferType != nil && *p.OfferType == 1,
		AutoRenew:             p.ExpiresDate != nil,
		ExpiresAt:             msToTime(p.ExpiresDate),
		RevokedAt:             msToTime(p.RevocationDate),

		LastSignedDate: time.UnixMilli(*p.SignedDate).UTC(),
	}, p.AppAccountToken, nil
}

func (s *Service) decodeNotification(jws string) (*notificationPayload, *Subscription, []byte, error) {
	if s.verifier == nil || s.cfg.BundleID == "" {
		return nil, nil, nil, ErrNotConfigured
	}
	raw, err := s.verifier.VerifyPayload(jws)
	if err != nil {
		return nil, nil, nil, ErrInvalidNotice
	}
	var n notificationPayload
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrInvalidNotice, err)
	}
	if n.NotificationUUID == "" {
		return nil, nil, nil, fmt.Errorf("%w: missing notificationUUID", ErrInvalidNotice)
	}
	if n.bundleID() != s.cfg.BundleID {
		return nil, nil, nil, fmt.Errorf("%w: bundle id mismatch", ErrInvalidNotice)
	}
	env, err := s.cfg.checkEnvironment(n.environment())
	if err != nil {
		return nil, nil, nil, ErrInvalidNotice
	}
	n.Data.Environment = env
	var sub *Subscription
	if n.Data.SignedTransactionInfo != "" {
		sub, _, err = s.decodeTransaction(n.Data.SignedTransactionInfo)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: nested transaction: %v", ErrInvalidNotice, err)
		}
	}
	return &n, sub, raw, nil
}
