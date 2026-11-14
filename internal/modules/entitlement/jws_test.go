package entitlement

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

const testBundle = "com.pickhour.app"

// newTestService wires a Service with the self-generated PKI and an in-memory
// repo. now is fixed so status derivation is deterministic.
func newTestService(t *testing.T, cfg Config) (*Service, *testPKI, *memRepo) {
	t.Helper()
	pki := newTestPKI(t, pkiOpts{})
	repo := newMemRepo()
	svc := NewService(repo, WithVerifier(pki.verifier(t), cfg))
	return svc, pki, repo
}

func defaultCfg() Config { return Config{BundleID: testBundle, AllowSandbox: true} }

func txPayload(fields string) string { return fmt.Sprintf(`{%s}`, fields) }

func TestDecodeTransactionOwnership(t *testing.T) {
	svc, pki, _ := newTestService(t, defaultCfg())
	expires := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	signed := time.Now().UnixMilli()
	base := fmt.Sprintf(`"originalTransactionId":"1000000000000001","productId":"com.pickhour.plus.yearly","expiresDate":%d,"signedDate":%d,"bundleId":%q,"environment":"Production"`, expires, signed, testBundle)

	cases := []struct {
		name    string
		extra   string
		wantOwn string
	}{
		{"purchased", `,"ownershipType":"PURCHASED"`, "purchased"},
		{"family shared", `,"ownershipType":"FAMILY_SHARED"`, "family_shared"},
		{"missing ownershipType defaults purchased", ``, "purchased"},
		{"unknown value defaults purchased", `,"ownershipType":"SOMETHING_ELSE"`, "purchased"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, _, err := svc.decodeTransaction(pki.sign(t, txPayload(base+tc.extra)))
			if err != nil {
				t.Fatal(err)
			}
			if sub.Ownership != tc.wantOwn || sub.Status != "active" {
				t.Fatalf("ownership=%q status=%q", sub.Ownership, sub.Status)
			}
		})
	}
}

func TestDecodeTransactionUsesAppleSignedDate(t *testing.T) {
	svc, pki, _ := newTestService(t, defaultCfg())
	signed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	jws := pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","productId":"p","signedDate":%d,"bundleId":%q,"environment":"Production"`, signed.UnixMilli(), testBundle)))
	sub, _, err := svc.decodeTransaction(jws)
	if err != nil {
		t.Fatal(err)
	}
	if !sub.LastSignedDate.Equal(signed) {
		t.Fatalf("LastSignedDate = %v, want payload signedDate %v (not time.Now)", sub.LastSignedDate, signed)
	}
}

func TestDecodeTransactionRejects(t *testing.T) {
	svc, pki, _ := newTestService(t, defaultCfg())
	now := time.Now().UnixMilli()
	mk := func(bundle, env string, extra string) string {
		return pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","productId":"p","signedDate":%d,"bundleId":%q,"environment":%q%s`, now, bundle, env, extra)))
	}
	cases := map[string]string{
		"wrong bundle":        mk("com.evil.app", "Production", ""),
		"xcode environment":   mk(testBundle, "Xcode", ""),
		"localtesting env":    mk(testBundle, "LocalTesting", ""),
		"missing environment": mk(testBundle, "", ""),
		"missing signedDate":  pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","productId":"p","bundleId":%q,"environment":"Production"`, testBundle))),
		"missing product":     pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","signedDate":%d,"bundleId":%q,"environment":"Production"`, now, testBundle))),
		"not json":            pki.sign(t, `nope`),
	}
	for name, jws := range cases {
		if _, _, err := svc.decodeTransaction(jws); !errors.Is(err, ErrInvalidJWS) {
			t.Errorf("%s: want ErrInvalidJWS, got %v", name, err)
		}
	}
}

func TestSandboxPolicy(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, allow := range []bool{true, false} {
		svc, pki, _ := newTestService(t, Config{BundleID: testBundle, AllowSandbox: allow})
		jws := pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","productId":"p","signedDate":%d,"bundleId":%q,"environment":"Sandbox"`, now, testBundle)))
		sub, _, err := svc.decodeTransaction(jws)
		switch {
		case allow && (err != nil || sub.Environment != "sandbox"):
			t.Errorf("allow=true: sub=%+v err=%v", sub, err)
		case !allow && !errors.Is(err, ErrInvalidJWS):
			t.Errorf("allow=false: want rejection, got %v", err)
		}
	}
}

func TestDecodeTransactionStatus(t *testing.T) {
	svc, pki, _ := newTestService(t, defaultCfg())
	now := time.Now()
	mk := func(extra string) string {
		return pki.sign(t, txPayload(fmt.Sprintf(`"originalTransactionId":"1","productId":"p","signedDate":%d,"bundleId":%q,"environment":"Production"%s`, now.UnixMilli(), testBundle, extra)))
	}
	cases := map[string]string{
		"revoked": mk(fmt.Sprintf(`,"revocationDate":%d`, now.UnixMilli())),
		"expired": mk(fmt.Sprintf(`,"expiresDate":%d`, now.Add(-time.Hour).UnixMilli())),
		"active":  mk(fmt.Sprintf(`,"expiresDate":%d`, now.Add(time.Hour).UnixMilli())),
	}
	for want, jws := range cases {
		sub, _, err := svc.decodeTransaction(jws)
		if err != nil || sub.Status != want {
			t.Errorf("%s: status=%v err=%v", want, sub, err)
		}
	}
}

func TestUnconfiguredFailsClosed(t *testing.T) {
	pki := newTestPKI(t, pkiOpts{})
	jws := pki.sign(t, `{}`)
	for name, svc := range map[string]*Service{
		"no verifier":  NewService(newMemRepo()),
		"no bundle id": NewService(newMemRepo(), WithVerifier(pki.verifier(t), Config{})),
		"nil verifier": NewService(newMemRepo(), WithVerifier(nil, Config{BundleID: testBundle})),
	} {
		if _, err := svc.LinkTransaction(t.Context(), "dev", jws); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("%s link: %v", name, err)
		}
		if err := svc.ProcessNotification(t.Context(), jws); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("%s notification: %v", name, err)
		}
	}
}
