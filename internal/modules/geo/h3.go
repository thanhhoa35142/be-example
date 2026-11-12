package geo

import (
	"fmt"

	"github.com/uber/h3-go/v4"
)

// H3Resolution is the grid resolution used for spot.cell_id.
const H3Resolution = 7

// WeatherResolution is the H3 resolution used for weather_hourly cells.
// It is coarser than H3Resolution on purpose: forecast models resolve at
// ~9-11 km, so fetching per res-7 spot cell would burn provider quota on
// near-identical forecasts. Set once at startup from WEATHER_H3_RES.
var WeatherResolution = 6

// WeatherCellID maps any cell index to its weather-grid parent.
// weather_hourly.cell_id stores cells at WeatherResolution; consumers map
// spot cells through this function before lookup. Falls back to the input
// on invalid cells so lookups degrade to a miss instead of an error.
func WeatherCellID(cellID int64) int64 {
	if WeatherResolution >= H3Resolution {
		return cellID
	}
	cell := h3.Cell(uint64(cellID))
	if !cell.IsValid() {
		return cellID
	}
	parent, err := cell.Parent(WeatherResolution)
	if err != nil {
		return cellID
	}
	return int64(parent)
}

// CellIDFromLatLng returns the H3 cell index at H3Resolution for a point.
func CellIDFromLatLng(lat, lng float64) (int64, error) {
	cell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lng}, H3Resolution)
	if err != nil {
		return 0, err
	}
	return int64(cell), nil
}

// CellCenter returns the lat/lng center of an H3 cell index.
func CellCenter(cellID int64) (lat, lng float64, err error) {
	cell := h3.Cell(uint64(cellID))
	if !cell.IsValid() {
		return 0, 0, fmt.Errorf("h3 cell %d: invalid index", cellID)
	}
	ll, err := cell.LatLng()
	if err != nil {
		return 0, 0, fmt.Errorf("h3 cell %d: %w", cellID, err)
	}
	return ll.Lat, ll.Lng, nil
}
