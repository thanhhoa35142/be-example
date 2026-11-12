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
