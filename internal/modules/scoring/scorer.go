package scoring

import "math"

type WeatherInput struct {
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
	WindKmh    *float64
	UV         *float64
	Cloud      *int
}

type CrowdInput struct {
	Level  int
	Source string
}

type Inputs struct {
	Weather          *WeatherInput
	Crowd            *CrowdInput
	AmenityScore     *int
	ShadeScore       *int
	DaysSinceUpdated int

	SunElevationDeg *float64
}

type Components struct {
	Weather *int `json:"weather,omitempty"`
	Crowd   *int `json:"crowd,omitempty"`
	Amenity *int `json:"amenity,omitempty"`
	Shade   *int `json:"shade,omitempty"`
	Fresh   *int `json:"fresh,omitempty"`
}

type Reason struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

type Result struct {
	Score      int
	Components Components
	CapReason  *string
	Reasons    []Reason
	Confidence int
	HasData    bool
}

func Score(in Inputs, cfg Config) Result {
	comps := Components{
		Weather: weatherScore(in.Weather),
		Crowd:   crowdScore(in.Crowd),
		Amenity: in.AmenityScore,
		Shade:   in.ShadeScore,
		Fresh:   freshScore(in.DaysSinceUpdated),
	}

	res := Result{Components: comps}

	hasReal := comps.Weather != nil || comps.Crowd != nil || comps.Amenity != nil || comps.Shade != nil
	if !hasReal {
		res.Confidence = 1
		return res
	}

	var total, wsum float64
	add := func(w float64, v *int) {
		if v != nil {
			total += w * float64(*v)
			wsum += w
		}
	}
	add(cfg.Weights.Weather, comps.Weather)
	add(cfg.Weights.Crowd, comps.Crowd)
	add(cfg.Weights.Amenity, comps.Amenity)
	add(cfg.Weights.Shade, comps.Shade)
	add(cfg.Weights.Fresh, comps.Fresh)

	res.HasData = true
	res.Score = int(math.Round(total / wsum))

	if r, capVal := hardCap(in, cfg); r != nil {
		res.CapReason = r
		if res.Score > capVal {
			res.Score = capVal
		}
	}

	res.Confidence = confidence(in, comps)
	res.Reasons = topReasons(in, comps, res.CapReason, cfg)
	return res
}

const (
	CapRain     = "rain"
	CapHeat     = "heat"
	CapCold     = "cold"
	CapDark     = "dark"
	CapTwilight = "twilight"
)

func hardCap(in Inputs, cfg Config) (*string, int) {
	var reason *string
	capVal := 0
	consider := func(r string, v int) {
		if reason == nil || v < capVal {
			reason, capVal = strPtr(r), v
		}
	}
	if r := weatherCapReason(in, cfg); r != nil {
		switch *r {
		case CapHeat:
			consider(*r, cfg.Thresholds.HeatScoreCap)
		case CapCold:
			consider(*r, cfg.Thresholds.ColdScoreCap)
		default:
			consider(*r, cfg.Thresholds.RainScoreCap)
		}
	}
	if r, v := daylightCap(in.SunElevationDeg, cfg.Thresholds); r != "" {
		consider(r, v)
	}
	return reason, capVal
}

func daylightCap(elev *float64, th Thresholds) (string, int) {
	if elev == nil {
		return "", 0
	}
	switch {
	case th.DarkScoreCap > 0 && *elev < th.DarkElevationDeg:
		return CapDark, th.DarkScoreCap
	case th.TwilightScoreCap > 0 && *elev < th.TwilightElevationDeg:
		return CapTwilight, th.TwilightScoreCap
	}
	return "", 0
}

func weatherCapReason(in Inputs, cfg Config) *string {
	if in.Weather == nil {
		return nil
	}
	if in.Weather.PrecipProb != nil && *in.Weather.PrecipProb >= cfg.Thresholds.RainProbThreshold {
		return strPtr(CapRain)
	}
	t := in.Weather.FeelsLikeC
	if t == nil {
		t = in.Weather.TempC
	}
	if t == nil {
		return nil
	}
	if *t >= cfg.Thresholds.HeatCapC {
		return strPtr(CapHeat)
	}
	if *t <= cfg.Thresholds.ColdCapC {
		return strPtr(CapCold)
	}
	return nil
}

func confidence(in Inputs, c Components) int {
	conf := 3
	if c.Weather == nil {
		conf--
	}
	if c.Crowd == nil {
		conf--
	} else if in.Crowd != nil && in.Crowd.Source == "model" {

		conf = min(conf, 2)
	}
	if c.Amenity == nil {
		conf--
	}
	return max(1, min(3, conf))
}

func weatherScore(w *WeatherInput) *int {
	if w == nil {
		return nil
	}
	t := w.FeelsLikeC
	if t == nil {
		t = w.TempC
	}
	if t == nil && w.PrecipProb == nil && w.WindKmh == nil && w.UV == nil {
		return nil
	}

	s := 100.0
	if t != nil {
		s -= math.Abs(*t-22) * 3.5
	}
	if w.PrecipProb != nil {
		s -= float64(*w.PrecipProb) * 0.6
	}
	if w.WindKmh != nil && *w.WindKmh > 30 {
		s -= (*w.WindKmh - 30) * 1.5
	}
	if w.UV != nil && *w.UV > 8 {
		s -= (*w.UV - 8) * 5
	}
	return intPtr(clampRound(s))
}

func crowdScore(c *CrowdInput) *int {
	if c == nil {
		return nil
	}
	return intPtr(clampRound(100 - float64(c.Level)))
}

func freshScore(days int) *int {
	var s int
	switch {
	case days <= 7:
		s = 100
	case days <= 14:
		s = 85
	case days <= 30:
		s = 60
	case days <= 90:
		s = 35
	default:
		s = 15
	}
	return intPtr(s)
}

func topReasons(in Inputs, c Components, capReason *string, cfg Config) []Reason {
	var out []Reason
	if capReason != nil {
		code := "cap." + *capReason
		out = append(out, Reason{Code: code})
	}

	w := in.Weather
	if w != nil {
		t := w.FeelsLikeC
		if t == nil {
			t = w.TempC
		}
		switch {
		case w.PrecipProb != nil && *w.PrecipProb >= 50:
			out = append(out, Reason{Code: "weather.rain_risk", Params: map[string]any{"prob": *w.PrecipProb}})
		case t != nil && *t >= 28:
			out = append(out, Reason{Code: "weather.hot", Params: map[string]any{"temp": round1(*t)}})
		case t != nil && *t <= 8:
			out = append(out, Reason{Code: "weather.cold", Params: map[string]any{"temp": round1(*t)}})
		case t != nil && *t >= 18 && *t <= 26 && (w.PrecipProb == nil || *w.PrecipProb < 30):
			out = append(out, Reason{Code: "weather.clear", Params: map[string]any{"temp": round1(*t)}})
		}
	}

	if in.Crowd != nil {
		switch {
		case in.Crowd.Level < 30:
			out = append(out, Reason{Code: "crowd.low"})
		case in.Crowd.Level <= 70:
			out = append(out, Reason{Code: "crowd.moderate"})
		default:
			out = append(out, Reason{Code: "crowd.high"})
		}
	}

	if c.Amenity != nil && *c.Amenity >= 60 {
		out = append(out, Reason{Code: "amenity.well_equipped"})
	}
	if in.DaysSinceUpdated <= 14 {
		out = append(out, Reason{Code: "fresh.recent"})
	}

	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func clampRound(f float64) int {
	return max(0, min(100, int(math.Round(f))))
}

func round1(f float64) float64 {
	return math.Round(f*10) / 10
}

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }
