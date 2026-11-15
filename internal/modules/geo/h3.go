package geo

import (
	"fmt"

	"github.com/uber/h3-go/v4"
)

const H3Resolution = 7

var WeatherResolution = 6

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

func CellIDFromLatLng(lat, lng float64) (int64, error) {
	cell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lng}, H3Resolution)
	if err != nil {
		return 0, err
	}
	return int64(cell), nil
}

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
