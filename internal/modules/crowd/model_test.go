package crowd

import "testing"

func TestBaseline(t *testing.T) {
	if Baseline("park", 0) != 0.55 {
		t.Fatal("park baseline")
	}
	if Baseline("unknown", 0) != 0.5 {
		t.Fatal("unknown kind fallback")
	}
	big := Baseline("park", 2_000_000) // ~200ha
	small := Baseline("park", 5_000)   // ~0.5ha
	if big <= small {
		t.Fatal("larger area should have higher baseline")
	}
}

func TestWeatherFactor(t *testing.T) {
	rain := 80
	if got := WeatherFactor(nil, nil, &rain); got > 0.6 {
		t.Fatalf("heavy rain should suppress crowd, got %v", got)
	}
	hot := 38.0
	if got := WeatherFactor(&hot, &hot, nil); got > 0.65 {
		t.Fatalf("extreme heat should suppress crowd, got %v", got)
	}
	pleasant := 20.0
	low := 10
	if got := WeatherFactor(&pleasant, &pleasant, &low); got < 1.0 {
		t.Fatalf("pleasant weather should boost crowd, got %v", got)
	}
	if got := WeatherFactor(nil, nil, nil); got != 1.0 {
		t.Fatalf("missing data must be neutral, got %v", got)
	}
}

func TestCalendarFactor(t *testing.T) {
	if CalendarFactor("public") != 1.30 {
		t.Fatal("public holiday boost")
	}
	if CalendarFactor("school") != 1.15 {
		t.Fatal("school holiday boost")
	}
	if CalendarFactor("") != 1.0 {
		t.Fatal("no holiday neutral")
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize(50, 100); got != 50 {
		t.Fatalf("got %d", got)
	}
	if got := Normalize(150, 100); got != 100 {
		t.Fatalf("cap at 100, got %d", got)
	}
	if got := Normalize(10, 0); got != 0 {
		t.Fatalf("zero peak → 0, got %d", got)
	}
}
