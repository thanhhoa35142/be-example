package entitlement

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// transactionPayload is the decoded JWS payload of a StoreKit 2
// signedTransactionInfo. Only fields we persist are modeled.
type transactionPayload struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	ProductID             string `json:"productId"`
	ExpiresDate           *int64 `json:"expiresDate"` // ms since epoch
	Environment           string `json:"environment"`
	OfferType             *int   `json:"offerType"`
	RevocationDate        *int64 `json:"revocationDate"`
	RevocationReason      *int   `json:"revocationReason"`
	AppAccountToken       string `json:"appAccountToken"`
	// OwnershipType is "PURCHASED" or "FAMILY_SHARED" for transactions the
	// device received through Apple's Family Sharing.
	OwnershipType string `json:"ownershipType"`
}

// notificationPayload is the decoded JWS payload of an App Store Server
// Notification V2.
type notificationPayload struct {
	NotificationType string `json:"notificationType"`
	Subtype          string `json:"subtype"`
	NotificationUUID string `json:"notificationUUID"`
	Data             struct {
		SignedTransactionInfo string `json:"signedTransactionInfo"`
	} `json:"data"`
	SignedDate  int64  `json:"signedDate"`
	Environment string `json:"environment"`
}

// decodeJWSPayload extracts and decodes the payload segment of a compact JWS.
// NOTE: signature verification against Apple's root CA is not performed here —
// production must verify x5c chain + signature before trusting the payload.
func decodeJWSPayload(jws string) ([]byte, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments", ErrInvalidJWS)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload base64: %v", ErrInvalidJWS, err)
	}
	return raw, nil
}

func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

// decodeTransaction parses a signedTransactionInfo JWS into a Subscription.
func decodeTransaction(jws string) (*Subscription, error) {
	raw, err := decodeJWSPayload(jws)
	if err != nil {
		return nil, err
	}
	var p transactionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJWS, err)
	}
	if p.OriginalTransactionID == "" || p.ProductID == "" {
		return nil, fmt.Errorf("%w: missing required fields", ErrInvalidJWS)
	}

	status := "active"
	switch {
	case p.RevocationDate != nil:
		status = "revoked"
	case p.ExpiresDate != nil && time.UnixMilli(*p.ExpiresDate).Before(time.Now()):
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
		Environment:           strings.ToLower(p.Environment),
		Ownership:             ownership,
		IsTrial:               p.OfferType != nil && *p.OfferType == 1, // introductory offer
		AutoRenew:             p.ExpiresDate != nil,
		ExpiresAt:             msToTime(p.ExpiresDate),
		RevokedAt:             msToTime(p.RevocationDate),
		LastSignedDate:        time.Now().UTC(),
	}, nil
}

// decodeNotification parses a signedPayload JWS (App Store Server
// Notification V2) into its header fields and optional transaction.
// Returns the decoded JSON payload for audit storage.
func decodeNotification(jws string) (*notificationPayload, *Subscription, []byte, error) {
	raw, err := decodeJWSPayload(jws)
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
	var sub *Subscription
	if n.Data.SignedTransactionInfo != "" {
		if s, err := decodeTransaction(n.Data.SignedTransactionInfo); err == nil {
			sub = s
		}
	}
	return &n, sub, raw, nil
}
