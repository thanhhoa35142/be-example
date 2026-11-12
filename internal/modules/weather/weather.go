package weather

import "time"

// Point is one hour of forecast for a grid cell.
type Point struct {
	TS         time.Time
	TempC      *float64
	FeelsLikeC *float64
	PrecipProb *int // percent 0-100
	WindKmh    *float64
	UV         *float64
	Cloud      *int // percent 0-100
}
