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

	ErrConflict = errors.New("conflict")

	ErrInvalidInput = errors.New("invalid device registration")
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
