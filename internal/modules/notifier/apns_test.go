package notifier

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
)

func TestProviderTokenIsValidES256JWT(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	c, err := newClient(Config{KeyID: "KEY1234567", TeamID: "TEAM123456", KeyPEM: pemStr, BundleID: "com.example.MyApp"})
	if err != nil {
		t.Fatal(err)
	}

	tok, err := c.providerToken()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT must have 3 parts, got %d", len(parts))
	}

	var header map[string]string
	if err := json.Unmarshal(mustB64(parts[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "ES256" || header["kid"] != "KEY1234567" {
		t.Fatalf("bad header: %v", header)
	}

	var claims map[string]any
	if err := json.Unmarshal(mustB64(parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "TEAM123456" {
		t.Fatalf("bad claims: %v", claims)
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Fatalf("iat missing: %v", claims)
	}

	// Verify signature: ES256 = raw R||S over SHA-256 of signing input.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature must be 64 raw bytes, got %d err=%v", len(sig), err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Fatal("JWT signature does not verify")
	}

	// Cached token is stable within the window.
	again, _ := c.providerToken()
	if again != tok {
		t.Fatal("provider token should be cached")
	}
}

func mustB64(s string) []byte {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
