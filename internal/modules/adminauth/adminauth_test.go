package adminauth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPasswordHashVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("garbage", "anything") {
		t.Fatal("malformed hash accepted")
	}

	hash2, _ := HashPassword("correct horse battery staple")
	if hash == hash2 {
		t.Fatal("salt not random")
	}
}

func TestCipherSealOpen(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "JBSWY3DPEHPK3PXP" {
		t.Fatal("seal did not encrypt")
	}
	plain, err := c.Open(sealed)
	if err != nil || string(plain) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %v %q", err, plain)
	}
	if _, err := c.Open([]byte("short")); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("16-byte key accepted")
	}
}

func TestValidateTOTP(t *testing.T) {
	secret, _, err := NewTOTPSecret("Pickhour", "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	counter, ok := ValidateTOTP(secret, code, now)
	if !ok {
		t.Fatal("fresh code rejected")
	}
	if counter != now.Unix()/30 {
		t.Fatalf("counter %d, want %d", counter, now.Unix()/30)
	}

	prev, _ := totp.GenerateCode(secret, now.Add(-30*time.Second))
	if _, ok := ValidateTOTP(secret, prev, now); !ok {
		t.Fatal("previous-step code rejected")
	}

	old, _ := totp.GenerateCode(secret, now.Add(-90*time.Second))
	if _, ok := ValidateTOTP(secret, old, now); ok {
		t.Fatal("out-of-window code accepted")
	}
	if _, ok := ValidateTOTP(secret, "000000", now); ok {
		t.Fatal("bad code accepted")
	}
	if _, ok := ValidateTOTP(secret, code+"0", now); ok {
		t.Fatal("7-digit code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("want 10 codes, got %d/%d", len(codes), len(hashes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("bad format %q", c)
		}
		if seen[c] {
			t.Fatal("duplicate code")
		}
		seen[c] = true

		if string(HashRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " ")))) != string(HashRecoveryCode(c)) {
			t.Fatalf("normalization mismatch for %q", c)
		}
	}
}
