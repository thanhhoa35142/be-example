package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("device not found")
	// ErrConflict is now only a lost race between concurrent registrations of
	// the same APNs token (a new device otherwise takes the token over).
	ErrConflict = errors.New("conflict")
	// ErrInvalidInput marks a registration payload that breaks a field limit.
	ErrInvalidInput = errors.New("invalid device registration")
)

// Field limits for POST /v1/devices.
const (
	MaxPrefsBytes = 4096 // serialized JSON size of prefs
	MaxLocaleLen  = 35
	MaxTZLen      = 64
	MaxVersionLen = 32
	MinTokenLen   = 32  // hex chars (16 bytes)
	MaxTokenLen   = 200 // hex chars (100 bytes, APNs' documented maximum)
)

var (
	localeRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,35}$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	tokenRe   = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// RegisterInput is the payload for POST /v1/devices.
// ID is generated client-side once and kept in the Keychain.
type RegisterInput struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string // "sandbox" | "production"
	Locale     *string
	TZ         *string
	AppVersion *string
	Prefs      map[string]any
}

// Device is the stored record (no location fields by design).
type Device struct {
	ID         uuid.UUID
	APNsToken  *string
	APNsEnv    *string
	Locale     *string
	TZ         *string
	AppVersion *string
	LastSeenAt time.Time
}

// Normalize trims empty optional strings to nil and enforces the field limits:
// locale/appVersion short safe charsets, tz a real IANA zone, apnsToken hex of
// bounded length, apnsEnv sandbox|production, prefs at most MaxPrefsBytes when
// serialized. It returns the cleaned copy.
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
