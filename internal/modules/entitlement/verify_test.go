package entitlement

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVerifyPayloadAccepts(t *testing.T) {
	pki := newTestPKI(t, pkiOpts{})
	got, err := pki.verifier(t).VerifyPayload(pki.sign(t, `{"hello":"world"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("payload = %s", got)
	}
}

func TestVerifyPayloadRejects(t *testing.T) {
	pki := newTestPKI(t, pkiOpts{})
	v := pki.verifier(t)
	good := pki.sign(t, `{"a":1}`)
	parts := strings.Split(good, ".")
	enc := base64.RawURLEncoding.EncodeToString

	otherPKI := newTestPKI(t, pkiOpts{})

	// ASN.1 DER signature instead of raw R||S.
	derJWS := func() string {
		d := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sig, _ := ecdsa.SignASN1(rand.Reader, pki.leafKey, d[:])
		return parts[0] + "." + parts[1] + "." + enc(sig)
	}()
	// Signature by a different key than the leaf's.
	wrongKey := func() string {
		d := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		r, s, _ := ecdsa.Sign(rand.Reader, otherPKI.leafKey, d[:])
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return parts[0] + "." + parts[1] + "." + enc(sig)
	}()
	// Valid signature but signature scalar replaced by r = s = 0.
	zero := parts[0] + "." + parts[1] + "." + enc(make([]byte, 64))

	hdr := func(mut func(map[string]any)) string {
		h := pki.header()
		mut(h)
		return pki.signWith(t, h, `{"a":1}`)
	}

	cases := map[string]struct {
		jws string
		v   *AppleVerifier
	}{
		"forged unsigned (old behaviour)": {enc([]byte(`{"alg":"ES256"}`)) + "." + enc([]byte(`{"a":1}`)) + "." + enc([]byte("sig")), v},
		"tampered payload":                {parts[0] + "." + enc([]byte(`{"a":2}`)) + "." + parts[2], v},
		"alg none":                        {hdr(func(h map[string]any) { h["alg"] = "none" }), v},
		"alg HS256":                       {hdr(func(h map[string]any) { h["alg"] = "HS256" }), v},
		"missing x5c":                     {hdr(func(h map[string]any) { delete(h, "x5c") }), v},
		"leaf only":                       {hdr(func(h map[string]any) { h["x5c"] = h["x5c"].([]string)[:1] }), v},
		"x5c not base64":                  {hdr(func(h map[string]any) { h["x5c"] = []string{"@@@", "@@@"} }), v},
		"untrusted root":                  {pki.sign(t, `{"a":1}`), otherPKI.verifier(t)},
		"DER signature":                   {derJWS, v},
		"other key signature":             {wrongKey, v},
		"zero signature":                  {zero, v},
		"short signature":                 {parts[0] + "." + parts[1] + "." + enc([]byte("sig")), v},
		"two segments":                    {parts[0] + "." + parts[1], v},
		"garbage":                         {"not a jws", v},
		"empty":                           {"", v},
	}
	for name, c := range cases {
		if _, err := c.v.VerifyPayload(c.jws); !errors.Is(err, ErrInvalidJWS) {
			t.Errorf("%s: want ErrInvalidJWS, got %v", name, err)
		}
	}
}

func TestVerifyPayloadChainPolicy(t *testing.T) {
	// Missing Apple marker OIDs: a certificate chained to a trusted root but
	// not an App Store signing certificate must be refused.
	for name, o := range map[string]pkiOpts{
		"no leaf OID":         {skipLeafOID: true},
		"no intermediate OID": {skipInterOID: true},
	} {
		pki := newTestPKI(t, o)
		if _, err := pki.verifier(t).VerifyPayload(pki.sign(t, `{}`)); !errors.Is(err, ErrInvalidJWS) {
			t.Errorf("%s: want rejection, got %v", name, err)
		}
	}

	// Expired leaf.
	pki := newTestPKI(t, pkiOpts{})
	v := pki.verifier(t)
	v.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, err := v.VerifyPayload(pki.sign(t, `{}`)); !errors.Is(err, ErrInvalidJWS) {
		t.Errorf("expired chain accepted: %v", err)
	}

	// A root shipped inside x5c is never trusted: with only the leaf's
	// self-made chain and an empty trust pool, verification fails.
	empty, _ := NewAppleVerifier(newTestPKI(t, pkiOpts{}).pool)
	if _, err := empty.VerifyPayload(pki.sign(t, `{}`)); !errors.Is(err, ErrInvalidJWS) {
		t.Errorf("x5c-supplied root was trusted: %v", err)
	}
}

func TestEmbeddedAppleRoot(t *testing.T) {
	if _, err := AppleRootPool(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(appleRootCAG3)
	const want = "63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179"
	if got := hexString(sum[:]); got != want {
		t.Fatalf("embedded root fingerprint %s, want %s", got, want)
	}
	// The real root must reject a chain from our test PKI.
	prod, err := NewProductionVerifier()
	if err != nil {
		t.Fatal(err)
	}
	pki := newTestPKI(t, pkiOpts{})
	if _, err := prod.VerifyPayload(pki.sign(t, `{}`)); !errors.Is(err, ErrInvalidJWS) {
		t.Fatalf("test chain accepted by production verifier: %v", err)
	}
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}
