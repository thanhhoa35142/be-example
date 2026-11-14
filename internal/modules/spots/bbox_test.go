package spots

import "testing"

func TestCanonicalBBox(t *testing.T) {
	// Nearby viewports snap to the same outward grid box.
	a := CanonicalBBox(BBox{West: 4.841, South: 52.341, East: 4.859, North: 52.359})
	b := CanonicalBBox(BBox{West: 4.843, South: 52.345, East: 4.857, North: 52.357})
	want := BBox{West: 4.84, South: 52.34, East: 4.86, North: 52.36}
	if a != want || b != want {
		t.Fatalf("got %v / %v want %v", a, b, want)
	}
	// Expansion must contain the raw box.
	raw := BBox{West: 4.851, South: 52.351, East: 4.901, North: 52.371}
	got := CanonicalBBox(raw)
	if got.West > raw.West || got.South > raw.South || got.East < raw.East || got.North < raw.North {
		t.Fatalf("canonical %v does not contain raw %v", got, raw)
	}
}

func TestCanonicalBBoxKeepsInvalidRaw(t *testing.T) {
	// west >= east: returned unchanged so the service still rejects it.
	bad := BBox{West: 5, South: 52, East: 4, North: 53}
	if got := CanonicalBBox(bad); got != bad {
		t.Fatalf("invalid bbox must pass through, got %v", got)
	}
	// Out of WGS84 bounds: unchanged.
	bad = BBox{West: -181, South: 52, East: 4, North: 53}
	if got := CanonicalBBox(bad); got != bad {
		t.Fatalf("out-of-bounds bbox must pass through, got %v", got)
	}
	// At the size cap, expansion would exceed it: keep raw.
	atCap := BBox{West: 4.001, South: 52, East: 4.501, North: 52.5}
	if got := CanonicalBBox(atCap); got != atCap {
		t.Fatalf("cap-sized bbox must pass through, got %v", got)
	}
}

func TestCanonicalBBoxClampsToWGS84(t *testing.T) {
	// A small valid box near the antimeridian/pole edges stays in bounds
	// and within the service size cap after outward snapping.
	got := CanonicalBBox(BBox{West: -179.999, South: 89.96, East: -179.97, North: 89.999})
	if got.West < -180 || got.East > 180 || got.South < -90 || got.North > 90 {
		t.Fatalf("canonical %v escapes WGS84", got)
	}
	if got.North != 90 {
		t.Fatalf("north should clamp to 90, got %v", got)
	}
	if validateBBox(got) != nil {
		t.Fatalf("canonical %v must remain valid", got)
	}
}
