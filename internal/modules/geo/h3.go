package geo

import (
	"fmt"

	"github.com/uber/h3-go/v4"
)

// H3Resolution is the grid resolution used for weather cells and spot.cell_id.
const H3Resolution = 7

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
