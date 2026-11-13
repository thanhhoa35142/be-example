package osm

import (
	"encoding/json"
	"testing"
)

// refBBox is a slow but obviously-correct recursive walker used to check
// the byte scanner.
func refBBox(t *testing.T, geom string) (w, s, e, n float64) {
	t.Helper()
	var g struct {
		Coordinates any `json:"coordinates"`
	}
	if err := json.Unmarshal([]byte(geom), &g); err != nil {
		t.Fatal(err)
	}
	w, s, e, n = 180, 90, -180, -90
	var walk func(any)
	walk = func(x any) {
		arr, ok := x.([]any)
		if !ok {
			return
		}
		if len(arr) >= 2 {
			if lon, ok := arr[0].(float64); ok {
				lat := arr[1].(float64)
				w, e, s, n = min(w, lon), max(e, lon), min(s, lat), max(n, lat)
				return
			}
		}
		for _, v := range arr {
			walk(v)
		}
	}
	walk(g.Coordinates)
	return
}

func TestGeoBBox(t *testing.T) {
	for _, geom := range []string{
		`{"type":"Point","coordinates":[4.2997042,52.0972003]}`,
		`{"type":"Point","coordinates": [ -0.5 , -1e-3 ]}`,
		// 2-point LineString and 2-polygon MultiPolygon: the old walker
		// treated any 2-element array as a coordinate pair and silently
		// dropped these features from the pack.
		`{"type":"LineString","coordinates":[[4.1,52.1],[4.3,52.4]]}`,
		`{"type":"Polygon","coordinates":[[[4,52],[5,52],[5,53],[4,52]],[[4.2,52.2],[4.3,52.2],[4.3,52.3],[4.2,52.2]]]}`,
		`{"type":"MultiPolygon","coordinates":[[[[4,52],[4.1,52],[4.1,52.1],[4,52]]],[[[6,51],[6.1,51],[6.1,51.1],[6,51]]]]}`,
	} {
		w, s, e, n, ok := geoBBox([]byte(geom))
		rw, rs, re, rn := refBBox(t, geom)
		if !ok || w != rw || s != rs || e != re || n != rn {
			t.Errorf("%s: got (%v,%v,%v,%v,%v) want (%v,%v,%v,%v)", geom, w, s, e, n, ok, rw, rs, re, rn)
		}
	}
	line := `{"type":"Feature","id":"n1","geometry":{"type":"Point","coordinates":[4.9,52.3]},"properties":{"name":"x"}}`
	if w, s, _, _, ok := geoBBox([]byte(line)); !ok || w != 4.9 || s != 52.3 {
		t.Errorf("full feature line: got %v %v %v", w, s, ok)
	}
	if _, _, _, _, ok := geoBBox([]byte(`{"coordinates":"oops"}`)); ok {
		t.Error("non-array coordinates must report !ok")
	}
}
