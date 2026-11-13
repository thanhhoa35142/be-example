package geo

import (
	"math"
	"time"
)

// SunElevationDeg returns the apparent solar elevation in degrees above the
// horizon (atmospheric refraction included) for a WGS84 latitude/longitude at
// the given instant. Longitude is positive east. Algorithm: NOAA General Solar
// Position Calculations (Meeus); accuracy is a few hundredths of a degree for
// 1900-2100, far better than the ~0.1° callers need.
func SunElevationDeg(latDeg, lonDeg float64, t time.Time) float64 {
	t = t.UTC()
	// Julian day and Julian century.
	jd := float64(t.Unix())/86400.0 + 2440587.5
	jc := (jd - 2451545.0) / 36525.0

	rad := math.Pi / 180
	geomMeanLon := math.Mod(280.46646+jc*(36000.76983+jc*0.0003032), 360)
	geomMeanAnom := 357.52911 + jc*(35999.05029-0.0001537*jc)
	eccent := 0.016708634 - jc*(0.000042037+0.0000001267*jc)
	m := geomMeanAnom * rad
	eqCenter := math.Sin(m)*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(2*m)*(0.019993-0.000101*jc) +
		math.Sin(3*m)*0.000289
	trueLon := geomMeanLon + eqCenter
	omega := 125.04 - 1934.136*jc
	appLon := trueLon - 0.00569 - 0.00478*math.Sin(omega*rad)

	meanObliq := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	obliqCorr := meanObliq + 0.00256*math.Cos(omega*rad)
	decl := math.Asin(math.Sin(obliqCorr*rad) * math.Sin(appLon*rad))

	y := math.Tan(obliqCorr*rad/2) * math.Tan(obliqCorr*rad/2)
	eqTime := 4 / rad * (y*math.Sin(2*geomMeanLon*rad) -
		2*eccent*math.Sin(m) +
		4*eccent*y*math.Sin(m)*math.Cos(2*geomMeanLon*rad) -
		0.5*y*y*math.Sin(4*geomMeanLon*rad) -
		1.25*eccent*eccent*math.Sin(2*m)) // minutes

	utcMin := float64(t.Hour()*60+t.Minute()) + float64(t.Second())/60 + float64(t.Nanosecond())/6e10
	trueSolarMin := math.Mod(utcMin+eqTime+4*lonDeg, 1440)
	if trueSolarMin < 0 {
		trueSolarMin += 1440
	}
	hourAngle := (trueSolarMin/4 - 180) * rad

	lat := latDeg * rad
	cosZ := math.Sin(lat)*math.Sin(decl) + math.Cos(lat)*math.Cos(decl)*math.Cos(hourAngle)
	cosZ = math.Max(-1, math.Min(1, cosZ))
	elev := 90 - math.Acos(cosZ)/rad

	// Atmospheric refraction correction (NOAA).
	var refr float64
	switch {
	case elev > 85:
		refr = 0
	case elev > 5:
		te := math.Tan(elev * rad)
		refr = 58.1/te - 0.07/(te*te*te) + 0.000086/(te*te*te*te*te)
	case elev > -0.575:
		refr = 1735 + elev*(-518.2+elev*(103.4+elev*(-12.79+elev*0.711)))
	default:
		refr = -20.772 / math.Tan(elev*rad)
	}
	return elev + refr/3600
}
