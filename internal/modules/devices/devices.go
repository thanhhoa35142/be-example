package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("device not found")

	ErrConflict = errors.New("conflict")

	ErrInvalidInput = errors.New("invalid device registration")

	// ErrUnauthorized means the device exists and holds a credential but the
	// request did not prove it (missing/invalid X-Device-Token).
	ErrUnauthorized = errors.New("device authentication required")
)

const (
	MaxPrefsBytes = 4096
	MaxLocaleLen  = 35
	MaxTZLen      = 64
	MaxVersionLen = 32
	MinTokenLen   = 32
	MaxTokenLen   = 200
)

var (
	localeRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,35}$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	tokenRe   = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

type RegisterInput struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string
	Locale     *string
	TZ         *string
	AppVersion *string
	Prefs      map[string]any

	// SecretHash is the SHA-256 hex of a freshly generated device secret,
	// set only when the row has no credential yet. TokenHash is the SHA-256
	// hex of the X-Device-Token the caller presented, used to authorize
	// re-registration of a device that already holds a credential.
	SecretHash string
	TokenHash  string
}

// NewSecret returns a fresh device secret and the SHA-256 hex used for
// storage/comparison. The raw secret is shown to the app exactly once.
func NewSecret() (raw, hashHex string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashSecret(raw), nil
}

// HashSecret is the storage form of a device secret: SHA-256 hex.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SecretMatches compares a presented secret against the stored hash in
// constant time.
func SecretMatches(secret, hashHex string) bool {
	return subtle.ConstantTimeCompare(
		[]byte(HashSecret(secret)), []byte(hashHex)) == 1
}

type Device struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string
	Locale     *string
	TZ         *string
	AppVersion *string
	LastSeenAt time.Time
}

func (in RegisterInput) Normalize() (RegisterInput, error) {
	blank := func(p *string) *string {
		if p == nil || *p == "" {
			return nil
		}
		return p
	}
	in.APNsToken, in.APNsEnv = blank(in.APNsToken), blank(in.APNsEnv)
	in.Locale, in.TZ, in.AppVersion = blank(in.Locale), blank(in.TZ), blank(in.AppVersion)

	bad := func(format string, a ...any) (RegisterInput, error) {
		return in, fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, a...))
	}
	if in.APNsEnv != nil && *in.APNsEnv != "sandbox" && *in.APNsEnv != "production" {
		return bad("apnsEnv must be sandbox or production")
	}
	if t := in.APNsToken; t != nil && (len(*t) < MinTokenLen || len(*t) > MaxTokenLen || !tokenRe.MatchString(*t)) {
		return bad("apnsToken must be %d-%d hex characters", MinTokenLen, MaxTokenLen)
	}
	if l := in.Locale; l != nil && !localeRe.MatchString(*l) {
		return bad("locale must be a language tag of at most %d characters", MaxLocaleLen)
	}
	if v := in.AppVersion; v != nil && !versionRe.MatchString(*v) {
		return bad("appVersion must be at most %d characters of [A-Za-z0-9._+-]", MaxVersionLen)
	}
	if z := in.TZ; z != nil && (len(*z) > MaxTZLen || *z == "Local" || !validZone(*z)) {
		return bad("tz must be an IANA time zone")
	}
	if len(in.Prefs) > 0 {
		b, err := json.Marshal(in.Prefs)
		if err != nil || len(b) > MaxPrefsBytes {
			return bad("prefs must serialize to at most %d bytes", MaxPrefsBytes)
		}
	}
	return in, nil
}

func validZone(tz string) bool {
	_, err := time.LoadLocation(tz)
	return err == nil
}
