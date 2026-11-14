package devices

import (
	"errors"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

func TestNormalizeLimits(t *testing.T) {
	good := RegisterInput{
		APNsToken: sp(strings.Repeat("ab", 32)), APNsEnv: sp("sandbox"),
		Locale: sp("vi-VN"), TZ: sp("Asia/Ho_Chi_Minh"), AppVersion: sp("1.2.3"),
		Prefs: map[string]any{"companions": []string{"kids"}},
	}
	if _, err := good.Normalize(); err != nil {
		t.Fatalf("good input rejected: %v", err)
	}
	// Empty optional strings are treated as absent.
	out, err := RegisterInput{TZ: sp(""), Locale: sp(""), APNsToken: sp("")}.Normalize()
	if err != nil || out.TZ != nil || out.Locale != nil || out.APNsToken != nil {
		t.Fatalf("empties: %+v %v", out, err)
	}

	bad := map[string]func(*RegisterInput){
		"env":           func(i *RegisterInput) { i.APNsEnv = sp("dev") },
		"token not hex": func(i *RegisterInput) { i.APNsToken = sp(strings.Repeat("zz", 32)) },
		"token short":   func(i *RegisterInput) { i.APNsToken = sp("abcd") },
		"token long":    func(i *RegisterInput) { i.APNsToken = sp(strings.Repeat("a", MaxTokenLen+2)) },
		"locale long":   func(i *RegisterInput) { i.Locale = sp(strings.Repeat("a", MaxLocaleLen+1)) },
		"locale chars":  func(i *RegisterInput) { i.Locale = sp("en US\n") },
		"tz unknown":    func(i *RegisterInput) { i.TZ = sp("Mars/Base") },
		"tz Local":      func(i *RegisterInput) { i.TZ = sp("Local") },
		"tz long":       func(i *RegisterInput) { i.TZ = sp(strings.Repeat("a", MaxTZLen+1)) },
		"version long":  func(i *RegisterInput) { i.AppVersion = sp(strings.Repeat("1", MaxVersionLen+1)) },
		"version chars": func(i *RegisterInput) { i.AppVersion = sp("1.0 beta") },
		"prefs too big": func(i *RegisterInput) { i.Prefs = map[string]any{"k": strings.Repeat("x", MaxPrefsBytes)} },
	}
	for name, mut := range bad {
		in := good
		mut(&in)
		if _, err := in.Normalize(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
