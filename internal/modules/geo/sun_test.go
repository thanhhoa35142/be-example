package geo

import (
	"math"
	"testing"
	"time"
)

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestSunElevationKnownValues(t *testing.T) {
	// Amsterdam 52.3676N 4.9041E, solstice solar noon ≈ 13:42 local CEST = 11:42 UTC.
	// Max elevation = 90 - 52.3676 + 23.44 ≈ 61.07 (+ refraction ~0.01).
	got := SunElevationDeg(52.3676, 4.9041, utc(2026, 6, 21, 11, 42))
	if math.Abs(got-61.1) > 0.3 {
		t.Errorf("Amsterdam solstice noon: got %.2f want ≈61.1", got)
	}
	if e := SunElevationDeg(52.3676, 4.9041, utc(2026, 6, 20, 23, 42)); e >= 0 {
		t.Errorf("Amsterdam solstice midnight should be negative, got %.2f", e)
	}
	// Sydney -33.8688, 151.2093; December solstice noon ≈ 12:00 AEDT-ish (01:00 UTC),
	// max ≈ 90 - 33.87 + 23.44 = 79.57.
	got = SunElevationDeg(-33.8688, 151.2093, utc(2026, 12, 21, 1, 53))
	if math.Abs(got-79.5) > 0.4 {
		t.Errorf("Sydney Dec noon: got %.2f want ≈79.5", got)
	}
	// Sydney June noon (~02:00 UTC): 90-33.87-23.44 = 32.7.
	got = SunElevationDeg(-33.8688, 151.2093, utc(2026, 6, 21, 2, 0))
	if math.Abs(got-32.7) > 0.5 {
		t.Errorf("Sydney Jun noon: got %.2f want ≈32.7", got)
	}
	// Equinox at the equator, solar noon on the prime meridian ≈ 90.
	got = SunElevationDeg(0, 0, utc(2026, 3, 20, 12, 5))
	if got < 87 {
		t.Errorf("equator equinox noon: got %.2f want ≈90", got)
	}
}

func TestSunElevationPolar(t *testing.T) {
	// Tromsø 69.6492N 18.9553E.
	for h := 0; h < 24; h++ {
		if e := SunElevationDeg(69.6492, 18.9553, utc(2026, 12, 21, h, 30)); e >= 0 {
			t.Errorf("Tromsø Dec 21 %02d:30Z elevation %.2f, want negative", h, e)
		}
		if e := SunElevationDeg(69.6492, 18.9553, utc(2026, 6, 21, h, 30)); e <= 0 {
			t.Errorf("Tromsø Jun 21 %02d:30Z elevation %.2f, want positive", h, e)
		}
	}
}
