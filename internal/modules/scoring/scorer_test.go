package scoring

import "testing"

func testConfig() Config {
	return Config{
		Version: 1,
		Weights: Weights{Weather: 0.40, Crowd: 0.25, Amenity: 0.20, Shade: 0.10, Fresh: 0.05},
		Thresholds: Thresholds{
			RainProbThreshold: 60, RainScoreCap: 40,
			HeatCapC: 35, HeatScoreCap: 30,
			ColdCapC: -10, ColdScoreCap: 30,
		},
	}
}

func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

func TestScorePerfectDay(t *testing.T) {
	res := Score(Inputs{
		Weather: &WeatherInput{
			FeelsLikeC: f64(22), PrecipProb: i(10), WindKmh: f64(10), UV: f64(3),
		},
		Crowd:            &CrowdInput{Level: 20, Source: "community"},
		AmenityScore:     i(80),
		DaysSinceUpdated: 3,
	}, testConfig())

	if !res.HasData {
		t.Fatal("expected HasData")
	}
	if res.Score < 80 {
		t.Fatalf("expected high score, got %d", res.Score)
	}
	if res.CapReason != nil {
		t.Fatalf("unexpected cap: %v", *res.CapReason)
	}
	if res.Confidence != 3 {
		t.Fatalf("expected confidence 3, got %d", res.Confidence)
	}
}

func TestScoreRainCap(t *testing.T) {
	res := Score(Inputs{
		Weather: &WeatherInput{
			FeelsLikeC: f64(22), PrecipProb: i(80),
		},
		Crowd:        &CrowdInput{Level: 10, Source: "provider"},
		AmenityScore: i(90),
	}, testConfig())

	if res.CapReason == nil || *res.CapReason != "rain" {
		t.Fatalf("expected rain cap, got %v", res.CapReason)
	}
	if res.Score > 40 {
		t.Fatalf("score %d exceeds rain cap 40", res.Score)
	}
}

func TestScoreHeatCap(t *testing.T) {
	res := Score(Inputs{
		Weather: &WeatherInput{FeelsLikeC: f64(38)},
	}, testConfig())
	if res.CapReason == nil || *res.CapReason != "heat" {
		t.Fatalf("expected heat cap, got %v", res.CapReason)
	}
	if res.Score > 30 {
		t.Fatalf("score %d exceeds heat cap 30", res.Score)
	}
}

func TestScoreModelCrowdCapsConfidence(t *testing.T) {
	res := Score(Inputs{
		Weather:      &WeatherInput{FeelsLikeC: f64(22), PrecipProb: i(10)},
		Crowd:        &CrowdInput{Level: 30, Source: "model"},
		AmenityScore: i(70),
	}, testConfig())
	if res.Confidence > 2 {
		t.Fatalf("model-only crowd must cap confidence at 2, got %d", res.Confidence)
	}
}

func TestScoreFreshAloneDoesNotScore(t *testing.T) {
	// Freshness boosts real scores but cannot score a spot-hour by itself.
	res := Score(Inputs{DaysSinceUpdated: 2}, testConfig())
	if res.HasData {
		t.Fatal("fresh-only input must not produce a score")
	}
}

func TestScoreMissingDataRenormalizes(t *testing.T) {
	// Only crowd present: its weight renormalizes to 1.0.
	res := Score(Inputs{
		Crowd:            &CrowdInput{Level: 0, Source: "community"},
		DaysSinceUpdated: 2,
	}, testConfig())
	if !res.HasData {
		t.Fatal("expected HasData via crowd component")
	}
	// crowd=100, fresh=100 → score 100
	if res.Score != 100 {
		t.Fatalf("expected score 100, got %d", res.Score)
	}
}

func TestScoreNoData(t *testing.T) {
	res := Score(Inputs{DaysSinceUpdated: 365}, testConfig())
	if res.HasData {
		t.Fatal("expected no data")
	}
	if res.Score != 0 {
		t.Fatalf("expected score 0, got %d", res.Score)
	}
}

func TestReasonsMaxThree(t *testing.T) {
	res := Score(Inputs{
		Weather:          &WeatherInput{FeelsLikeC: f64(22), PrecipProb: i(80)},
		Crowd:            &CrowdInput{Level: 10, Source: "community"},
		AmenityScore:     i(90),
		DaysSinceUpdated: 2,
	}, testConfig())
	if len(res.Reasons) > 3 {
		t.Fatalf("expected at most 3 reasons, got %d", len(res.Reasons))
	}
	if len(res.Reasons) == 0 {
		t.Fatal("expected at least one reason")
	}
}

func daylightConfig() Config {
	c := testConfig()
	c.Version = 2
	c.Thresholds.DarkElevationDeg, c.Thresholds.DarkScoreCap = -6, 10
	c.Thresholds.TwilightElevationDeg, c.Thresholds.TwilightScoreCap = 0, 40
	return c
}

func goodDay(elev *float64, precip int) Inputs {
	return Inputs{
		Weather:         &WeatherInput{FeelsLikeC: f64(22), PrecipProb: i(precip)},
		Crowd:           &CrowdInput{Level: 5, Source: "community"},
		AmenityScore:    i(90),
		SunElevationDeg: elev,
	}
}

func TestScoreDaylightCaps(t *testing.T) {
	cases := []struct {
		name   string
		elev   float64
		reason string
		max    int
	}{
		{"day", 30, "", 100},
		{"horizon", 0, "", 100},
		{"civil twilight", -3, "twilight", 40},
		{"edge -6", -6, "twilight", 40},
		{"dark", -6.01, "dark", 10},
		{"night", -40, "dark", 10},
	}
	for _, tc := range cases {
		res := Score(goodDay(f64(tc.elev), 5), daylightConfig())
		got := ""
		if res.CapReason != nil {
			got = *res.CapReason
		}
		if got != tc.reason || res.Score > tc.max {
			t.Errorf("%s: reason=%q score=%d, want reason=%q max=%d", tc.name, got, res.Score, tc.reason, tc.max)
		}
		if tc.reason != "" && (len(res.Reasons) == 0 || res.Reasons[0].Code != "cap."+tc.reason) {
			t.Errorf("%s: first reason = %v", tc.name, res.Reasons)
		}
	}
}

func TestScoreDaylightLowerCapWins(t *testing.T) {
	// rain cap 40 vs twilight cap 40: tie keeps the weather reason.
	res := Score(goodDay(f64(-3), 80), daylightConfig())
	if res.CapReason == nil || *res.CapReason != "rain" {
		t.Fatalf("tie: want rain, got %v", res.CapReason)
	}
	// dark (10) beats rain (40).
	res = Score(goodDay(f64(-20), 80), daylightConfig())
	if res.CapReason == nil || *res.CapReason != "dark" || res.Score > 10 {
		t.Fatalf("want dark<=10, got %v %d", res.CapReason, res.Score)
	}
	// heat cap 30 beats twilight 40.
	in := goodDay(f64(-3), 5)
	in.Weather.FeelsLikeC = f64(38)
	res = Score(in, daylightConfig())
	if res.CapReason == nil || *res.CapReason != "heat" {
		t.Fatalf("want heat, got %v", res.CapReason)
	}
}

func TestScoreDaylightDisabledInV1AndNilElevation(t *testing.T) {
	if res := Score(goodDay(f64(-40), 5), testConfig()); res.CapReason != nil {
		t.Fatalf("v1 config must not cap darkness, got %v", *res.CapReason)
	}
	if res := Score(goodDay(nil, 5), daylightConfig()); res.CapReason != nil {
		t.Fatalf("nil elevation must not cap, got %v", *res.CapReason)
	}
}
