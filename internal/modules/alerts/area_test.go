package alerts

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

func baseInput() RuleInput {
	r := 5000
	return RuleInput{RadiusM: &r, MinScore: 50, DaysMask: 127, StartLocal: "08:00", EndLocal: "10:00", TZ: "Europe/Amsterdam"}
}

func TestAreaCellRoundsBeforeConverting(t *testing.T) {
	// Different raw values that round to the same 2-decimal point share a cell.
	a, err := AreaCell(LatLon{Lat: 52.3612, Lon: 4.8791})
	if err != nil {
		t.Fatal(err)
	}
	b, err := AreaCell(LatLon{Lat: 52.36, Lon: 4.88})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected same cell, got %d vs %d", a, b)
	}
	want, _ := geo.CellIDFromLatLng(52.36, 4.88)
	if int64(a) != want {
		t.Fatalf("cell %d != res-%d cell of rounded point %d", a, geo.H3Resolution, want)
	}
	lat, lon, err := geo.CellCenter(int64(a))
	if err != nil || math.Abs(lat-52.36) > 0.03 || math.Abs(lon-4.88) > 0.03 {
		t.Fatalf("center %v,%v err=%v not near input", lat, lon, err)
	}
}

func TestAreaCellRange(t *testing.T) {
	for _, p := range []LatLon{{91, 0}, {-90.01, 0}, {0, 180.5}, {0, -181}, {math.NaN(), 0}, {0, math.Inf(1)}} {
		if _, err := AreaCell(p); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%v: want ErrInvalidRule, got %v", p, err)
		}
	}
	for _, p := range []LatLon{{90, 180}, {-90, -180}, {0, 0}, {-0.004, 0}} {
		if _, err := AreaCell(p); err != nil {
			t.Errorf("%v: %v", p, err)
		}
	}
}

func TestNormalizeShapes(t *testing.T) {
	spot := uuid.New()
	cell := uint64(0x871f1d489ffffff)
	area := &LatLon{Lat: 52.36, Lon: 4.88}

	// area -> cell, raw point dropped
	in := baseInput()
	in.Area = area
	out, err := normalize(in)
	if err != nil || out.H3Cell == nil || out.Area != nil {
		t.Fatalf("area: %+v err=%v", out, err)
	}

	// exactly one required
	for name, mut := range map[string]func(*RuleInput){
		"none":       func(*RuleInput) {},
		"spot+cell":  func(i *RuleInput) { i.SpotID = &spot; i.H3Cell = &cell },
		"spot+area":  func(i *RuleInput) { i.SpotID = &spot; i.Area = area },
		"cell+area":  func(i *RuleInput) { i.H3Cell = &cell; i.Area = area },
		"all three":  func(i *RuleInput) { i.SpotID = &spot; i.H3Cell = &cell; i.Area = area },
		"area no r":  func(i *RuleInput) { i.Area = area; i.RadiusM = nil },
		"area r=0":   func(i *RuleInput) { i.Area = area; z := 0; i.RadiusM = &z },
		"area r big": func(i *RuleInput) { i.Area = area; z := 50001; i.RadiusM = &z },
		"spot+r":     func(i *RuleInput) { i.SpotID = &spot },
		"area oor":   func(i *RuleInput) { i.Area = &LatLon{Lat: 100, Lon: 0} },
	} {
		in := baseInput()
		mut(&in)
		if _, err := normalize(in); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%s: want ErrInvalidRule, got %v", name, err)
		}
	}

	// spot rule without radius and cell rule still fine
	in = baseInput()
	in.SpotID = &spot
	in.RadiusM = nil
	if _, err := normalize(in); err != nil {
		t.Errorf("spot rule: %v", err)
	}
	in = baseInput()
	in.H3Cell = &cell
	if _, err := normalize(in); err != nil {
		t.Errorf("cell rule: %v", err)
	}
}

func TestRuleAreaCenter(t *testing.T) {
	if _, _, ok := (Rule{}).AreaCenter(); ok {
		t.Fatal("spot rule must have no center")
	}
	c, _ := AreaCell(LatLon{Lat: 52.36, Lon: 4.88})
	if _, _, ok := (Rule{H3Cell: &c}).AreaCenter(); !ok {
		t.Fatal("area rule must have a center")
	}
}
