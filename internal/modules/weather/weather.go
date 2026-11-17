package weather

import "time"

type Point struct {
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int
	// PrecipMM is the hourly precipitation amount in mm (sum over the
	// preceding hour). nil when the provider does not supply it.
	PrecipMM *float64
	WindKmh  *float64
	UV       *float64
	Cloud    *int
}

func (p Point) hasData() bool {
	return p.TempC != nil || p.FeelsLikeC != nil || p.PrecipProb != nil || p.PrecipMM != nil ||
		p.WindKmh != nil || p.UV != nil || p.Cloud != nil
}
