package adminauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
)

type Config struct {
	ChallengeTTL time.Duration
	SessionTTL   time.Duration
	IdleTTL      time.Duration
	MaxFailures  int
	Lockout      time.Duration
}

func DefaultConfig() Config {
	return Config{
		ChallengeTTL: 5 * time.Minute,
		SessionTTL:   12 * time.Hour,
		IdleTTL:      30 * time.Minute,
		MaxFailures:  5,
		Lockout:      15 * time.Minute,
	}
}

const (
	totpPeriod   = 30
	totpSkew     = 1
	totpDigits   = 6
	recoverySize = 10
)

type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("admin auth key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < c.aead.NonceSize() {
		return nil, fmt.Errorf("sealed secret too short")
	}
	return c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
}

func NewChallengeToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func NewSessionToken() (token string, hash []byte, err error) {
	return NewChallengeToken()
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func NewTOTPSecret(issuer, account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      otp.Digits(totpDigits),
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func ValidateTOTP(secretB32, code string, now time.Time) (counter int64, ok bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	base := now.Unix() / totpPeriod
	for dc := int64(-totpSkew); dc <= totpSkew; dc++ {
		c := base + dc
		if c < 0 {
			continue
		}
		if hotp.Validate(code, uint64(c), secretB32) {
			return c, true
		}
	}
	return 0, false
}

const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func GenerateRecoveryCodes() (codes []string, hashes [][]byte, err error) {
	for i := 0; i < recoverySize; i++ {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		for j, c := range buf {
			if j == 4 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		code := b.String()
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

func NormalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func HashRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return sum[:]
}
