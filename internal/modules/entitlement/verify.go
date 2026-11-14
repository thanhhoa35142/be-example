package entitlement

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// appleRootCAG3 is Apple Root CA - G3, the trust anchor of every App Store
// signed payload (StoreKit 2 JWS and Server Notifications V2). Downloaded from
// https://www.apple.com/certificateauthority/AppleRootCA-G3.cer
// SHA-256: 63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179
//
//go:embed certs/AppleRootCA-G3.cer
var appleRootCAG3 []byte

// AppleRootPool returns a pool containing only the embedded Apple Root CA G3.
func AppleRootPool() (*x509.CertPool, error) {
	cert, err := x509.ParseCertificate(appleRootCAG3)
	if err != nil {
		return nil, fmt.Errorf("parse embedded Apple root: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool, nil
}

// Marker extensions Apple puts on the certificates that sign App Store
// payloads (same checks as Apple's official server libraries): the leaf must
// carry the "Mac App Store receipt signing" OID and the intermediate the
// "WWDR intermediate" OID. This stops any other Apple-issued certificate
// (e.g. a developer certificate chained to the same root) from being accepted.
var (
	oidAppleLeaf         = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	oidAppleIntermediate = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// PayloadVerifier authenticates a compact JWS and returns its raw payload.
// Production uses AppleVerifier; tests inject one backed by a self-generated
// root. There is deliberately no implementation that skips verification.
type PayloadVerifier interface {
	VerifyPayload(jws string) ([]byte, error)
}

// AppleVerifier verifies ES256 JWS signed by a leaf certificate that chains
// (leaf -> intermediate -> trusted root) to Apple Root CA G3.
type AppleVerifier struct {
	roots *x509.CertPool
	now   func() time.Time
}

// NewAppleVerifier builds a verifier trusting the given roots. Use
// AppleRootPool() in production.
func NewAppleVerifier(roots *x509.CertPool) (*AppleVerifier, error) {
	if roots == nil {
		return nil, errors.New("entitlement: nil root pool")
	}
	return &AppleVerifier{roots: roots, now: time.Now}, nil
}

// NewProductionVerifier is NewAppleVerifier with the embedded Apple root.
func NewProductionVerifier() (*AppleVerifier, error) {
	pool, err := AppleRootPool()
	if err != nil {
		return nil, err
	}
	return NewAppleVerifier(pool)
}

type jwsHeader struct {
	Alg string   `json:"alg"`
	X5C []string `json:"x5c"`
}

// VerifyPayload checks the header (alg ES256, x5c chain), the certificate
// chain against the trusted roots (validity evaluated at the current time —
// the payload's own timestamps are never used to pick the verification time)
// and the raw R||S signature over "header.payload", then returns the decoded
// payload bytes. The root inside x5c, if any, is ignored: trust comes only
// from the configured roots.
func (v *AppleVerifier) VerifyPayload(jws string) ([]byte, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments", ErrInvalidJWS)
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header base64", ErrInvalidJWS)
	}
	var hdr jwsHeader
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header json", ErrInvalidJWS)
	}
	if hdr.Alg != "ES256" {
		return nil, fmt.Errorf("%w: unsupported alg", ErrInvalidJWS)
	}
	if len(hdr.X5C) < 2 || len(hdr.X5C) > 4 {
		return nil, fmt.Errorf("%w: bad x5c chain length", ErrInvalidJWS)
	}
	certs := make([]*x509.Certificate, len(hdr.X5C))
	for i, b64 := range hdr.X5C {
		der, err := base64.StdEncoding.DecodeString(b64) // x5c is std base64, not URL
		if err != nil {
			return nil, fmt.Errorf("%w: x5c base64", ErrInvalidJWS)
		}
		if certs[i], err = x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("%w: x5c certificate", ErrInvalidJWS)
		}
	}
	leaf := certs[0]
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   v.now(),
		// Apple's leaf is not a TLS server certificate; the default
		// (ServerAuth) would reject it.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("%w: certificate chain: %v", ErrInvalidJWS, err)
	}
	if !hasExtension(leaf, oidAppleLeaf) || !hasExtension(certs[1], oidAppleIntermediate) {
		return nil, fmt.Errorf("%w: certificates are not App Store signing certificates", ErrInvalidJWS)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve.Params().BitSize != 256 {
		return nil, fmt.Errorf("%w: leaf key must be ECDSA P-256", ErrInvalidJWS)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 { // raw R||S, 32 bytes each (not ASN.1)
		return nil, fmt.Errorf("%w: bad signature encoding", ErrInvalidJWS)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return nil, fmt.Errorf("%w: signature mismatch", ErrInvalidJWS)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload base64", ErrInvalidJWS)
	}
	return payload, nil
}

func hasExtension(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return true
		}
	}
	return false
}
