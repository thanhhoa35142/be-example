package crowd

import "math"

type CurveKey struct {
	Kind string
	Dow  int
	Hour int
}

var kindBaselines = map[string]float64{
	"park":  0.55,
	"beach": 0.60,
	"site":  0.60,
	"table": 0.50,
}

func Baseline(kind string, areaM2 float64) float64 {
	b, ok := kindBaselines[kind]
	if !ok {
		b = 0.5
	}
	if areaM2 <= 0 {
		return b
	}

	areaFactor := 0.75 + 0.25*math.Tanh(math.Log10(areaM2/10000+1))
	return b * areaFactor
}

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
		f *= 1.10
	}
	return math.Min(1.3, math.Max(0.3, f))
}

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
