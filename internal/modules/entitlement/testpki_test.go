package entitlement

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

// testPKI is a self-generated stand-in for Apple's chain: a P-384 root, a
// P-384 intermediate carrying the WWDR marker OID and a P-256 leaf carrying the
// receipt-signing marker OID. Nothing in production trusts it.
type testPKI struct {
	rootCert  *x509.Certificate
	interCert *x509.Certificate
	leafCert  *x509.Certificate
	leafKey   *ecdsa.PrivateKey
	pool      *x509.CertPool
}

type pkiOpts struct {
	skipLeafOID  bool
	skipInterOID bool
	leafNotAfter time.Time // zero = +1h
}

func mustKey(t *testing.T, c elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newTestPKI(t *testing.T, o pkiOpts) *testPKI {
	t.Helper()
	now := time.Now()
	rootKey, interKey, leafKey := mustKey(t, elliptic.P384()), mustKey(t, elliptic.P384()), mustKey(t, elliptic.P256())
	marker := func(oid asn1.ObjectIdentifier) []pkix.Extension {
		return []pkix.Extension{{Id: oid, Value: []byte{0x05, 0x00}}}
	}
	mk := func(serial int64, cn string, tmplMod func(*x509.Certificate), pub *ecdsa.PublicKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) *x509.Certificate {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    now.Add(-time.Hour),
			NotAfter:     now.Add(time.Hour),
		}
		tmplMod(tmpl)
		if parent == nil {
			parent = tmpl
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	root := mk(1, "Test Root G3", func(c *x509.Certificate) {
		c.IsCA, c.BasicConstraintsValid = true, true
		c.KeyUsage = x509.KeyUsageCertSign
	}, &rootKey.PublicKey, nil, rootKey)
	inter := mk(2, "Test WWDR", func(c *x509.Certificate) {
		c.IsCA, c.BasicConstraintsValid = true, true
		c.KeyUsage = x509.KeyUsageCertSign
		if !o.skipInterOID {
			c.ExtraExtensions = marker(oidAppleIntermediate)
		}
	}, &interKey.PublicKey, root, rootKey)
	leaf := mk(3, "Test Leaf", func(c *x509.Certificate) {
		c.KeyUsage = x509.KeyUsageDigitalSignature
		if !o.skipLeafOID {
			c.ExtraExtensions = marker(oidAppleLeaf)
		}
		if !o.leafNotAfter.IsZero() {
			c.NotAfter = o.leafNotAfter
		}
	}, &leafKey.PublicKey, inter, interKey)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &testPKI{rootCert: root, interCert: inter, leafCert: leaf, leafKey: leafKey, pool: pool}
}

func (p *testPKI) verifier(t *testing.T) *AppleVerifier {
	t.Helper()
	v, err := NewAppleVerifier(p.pool)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// signWith builds a compact JWS with the given header fields and raw R||S.
func (p *testPKI) signWith(t *testing.T, header map[string]any, payload string) string {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(header)
	signingInput := enc(h) + "." + enc([]byte(payload))
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, p.leafKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + enc(sig)
}

func (p *testPKI) header() map[string]any {
	return map[string]any{
		"alg": "ES256",
		"x5c": []string{b64(p.leafCert.Raw), b64(p.interCert.Raw), b64(p.rootCert.Raw)},
	}
}

// sign is the normal, valid JWS over payload.
func (p *testPKI) sign(t *testing.T, payload string) string {
	t.Helper()
	return p.signWith(t, p.header(), payload)
}
