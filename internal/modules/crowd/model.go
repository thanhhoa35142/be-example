package crowd

import "math"

// CurveKey identifies one slot of the weekly crowd curve.
type CurveKey struct {
	Kind string
	Dow  int // 0=Sunday .. 6=Saturday (time.Weekday convention)
	Hour int // 0-23, spot-local time
}

// kindBaselines are starting guesses for absolute busyness per spot kind,
// adjusted by area. Values are assumptions to be tuned with real data (spec §16).
var kindBaselines = map[string]float64{
	"park":  0.55,
	"beach": 0.60,
	"site":  0.60,
	"table": 0.50,
}

// Baseline estimates how busy a spot tends to be, from kind and area.
// Larger areas attract more people; factor stays within ~[0.75, 1.25].
func Baseline(kind string, areaM2 float64) float64 {
	b, ok := kindBaselines[kind]
	if !ok {
		b = 0.5
	}
	if areaM2 <= 0 {
		return b
	}
	// log-scaled: ~1ha → ~0.83, ~10ha → ~1.0, ~100ha → ~1.25
	areaFactor := 0.75 + 0.25*math.Tanh(math.Log10(areaM2/10000+1))
	return b * areaFactor
}

// WeatherFactor scales crowd by conditions: rain and extremes empty parks,
// mild pleasant weather gives a small boost. Neutral (1.0) without data.
func WeatherFactor(tempC, feelsLikeC *float64, precipProb *int) float64 {
	f := 1.0
	if precipProb != nil {
		switch {
		case *precipProb >= 60:
			f *= 0.55
		case *precipProb >= 30:
			f *= 0.75
		}
	}
	fl := feelsLikeC
	if fl == nil {
		fl = tempC
	}
	if fl != nil {
		switch {
		case *fl >= 35:
			f *= 0.60
		case *fl >= 30:
			f *= 0.80
		}
		switch {
		case *fl <= 0:
			f *= 0.60
		case *fl <= 5:
			f *= 0.80
		}
	}
	if tempC != nil && *tempC >= 15 && *tempC <= 25 &&
		(precipProb == nil || *precipProb < 20) {
		f *= 1.10 // pleasant bonus
	}
	return math.Min(1.3, math.Max(0.3, f))
}

// CalendarFactor: public holidays draw bigger crowds, school holidays mildly.
func CalendarFactor(holidayKind string) float64 {
	switch holidayKind {
	case "public":
		return 1.30
	case "school":
		return 1.15
	default:
		return 1.0
	}
}

// Normalize scales a raw estimate to 0-100 against the peak of the same
// kind within the same city pack (spec: "chuẩn hóa theo đỉnh của từng loại
// điểm trong cùng thành phố").
func Normalize(raw, kindCityPeak float64) int {
	if kindCityPeak <= 0 {
		return 0
	}
	v := 100 * raw / kindCityPeak
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(math.Round(v))
}
