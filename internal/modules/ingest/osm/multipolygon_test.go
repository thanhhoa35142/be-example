package osm

import (
	"encoding/json"
	"testing"
)

func pts(c ...float64) []ovPoint {
	out := make([]ovPoint, 0, len(c)/2)
	for i := 0; i+1 < len(c); i += 2 {
		out = append(out, ovPoint{Lon: c[i], Lat: c[i+1]})
	}
	return out
}

type mpGeo struct {
	Type        string           `json:"type"`
	Coordinates [][][][2]float64 `json:"coordinates"`
}

func decodeMP(t *testing.T, b []byte) mpGeo {
	t.Helper()
	var g mpGeo
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("invalid geojson %s: %v", b, err)
	}
	return g
}

// An outer ring split over two ways (the usual shape of a big park such as
// Vondelpark) plus a pond hole must come out as one MultiPolygon with one
// hole, not a vertex-mean Point.
func TestMultipolygonStitchesSplitOuterAndHole(t *testing.T) {
	members := []overpassMember{
		{Type: "way", Role: "outer", Geometry: pts(0, 0, 4, 0, 4, 4)},
		// second half is stored in the opposite direction
		{Type: "way", Role: "outer", Geometry: pts(0, 0, 0, 4, 4, 4)},
		{Type: "way", Role: "inner", Geometry: pts(1, 1, 2, 1, 2, 2, 1, 2, 1, 1)},
		{Type: "node", Role: "label", Geometry: nil},
	}
	b := multipolygonGeoJSON(members)
	if b == nil {
		t.Fatal("expected a MultiPolygon")
	}
	g := decodeMP(t, b)
	if g.Type != "MultiPolygon" || len(g.Coordinates) != 1 {
		t.Fatalf("want 1 polygon, got %s", b)
	}
	rings := g.Coordinates[0]
	if len(rings) != 2 {
		t.Fatalf("want outer + 1 hole, got %d rings", len(rings))
	}
	outer := rings[0]
	if outer[0] != outer[len(outer)-1] || len(outer) != 5 {
		t.Fatalf("outer ring not closed/stitched: %v", outer)
	}
}

func TestMultipolygonTwoOutersKeepsHolesWithTheirOuter(t *testing.T) {
	members := []overpassMember{
		{Type: "way", Role: "outer", Geometry: pts(0, 0, 4, 0, 4, 4, 0, 4, 0, 0)},
		{Type: "way", Role: "outer", Geometry: pts(10, 0, 14, 0, 14, 4, 10, 4, 10, 0)},
		{Type: "way", Role: "inner", Geometry: pts(11, 1, 12, 1, 12, 2, 11, 2, 11, 1)},
	}
	g := decodeMP(t, multipolygonGeoJSON(members))
	if len(g.Coordinates) != 2 || len(g.Coordinates[0]) != 1 || len(g.Coordinates[1]) != 2 {
		t.Fatalf("hole must belong to the second outer: %+v", g.Coordinates)
	}
}

func TestMultipolygonUnclosedFallsBack(t *testing.T) {
	members := []overpassMember{
		{Type: "way", Role: "outer", Geometry: pts(0, 0, 4, 0, 4, 4)}, // missing the other half
	}
	if b := multipolygonGeoJSON(members); b != nil {
		t.Fatalf("partial relation must not produce a polygon, got %s", b)
	}
}

func TestElementGeoJSONRelationKeepsOutline(t *testing.T) {
	rel := overpassElement{
		Type: "relation", ID: 1,
		Tags: map[string]string{"type": "multipolygon", "leisure": "park"},
		Members: []overpassMember{
			{Type: "way", Role: "outer", Geometry: pts(0, 0, 4, 0, 4, 4)},
			{Type: "way", Role: "outer", Geometry: pts(4, 4, 0, 4, 0, 0)},
		},
	}
	if g := decodeMP(t, elementGeoJSON(rel)); g.Type != "MultiPolygon" {
		t.Fatalf("relation should keep its outline, got %v", g.Type)
	}

	rel.Members = rel.Members[:1]
	var p struct{ Type string }
	if err := json.Unmarshal(elementGeoJSON(rel), &p); err != nil || p.Type != "Point" {
		t.Fatalf("unclosable relation falls back to a point, got %v %v", p.Type, err)
	}
}
