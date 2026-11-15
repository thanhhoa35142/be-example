package spots

import (
	"reflect"
	"testing"
)

func TestParseFilter(t *testing.T) {
	bad := []struct{ kinds, amenities, minScore string }{
		{"forest", "", ""},
		{"park,", "", ""},
		{"Park", "", ""},
		{"", "toilet,sauna", ""},
		{"", "toilet,,water", ""},
		{"", "", "abc"},
		{"", "", "-1"},
		{"", "", "101"},
		{"", "", "4.5"},
	}
	for _, b := range bad {
		if _, err := ParseFilter(b.kinds, b.amenities, b.minScore); err == nil {
			t.Errorf("%+v: want error", b)
		}
	}

	f, err := ParseFilter("", "", "")
	if err != nil || len(f.Kinds) != 0 || len(f.Amenities) != 0 || f.MinScore != nil {
		t.Fatalf("empty must be no filter: %+v %v", f, err)
	}
	f, err = ParseFilter("park, beach,park", "toilet,bicycle_parking,dog_area", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Kinds, []string{"park", "beach"}) ||
		!reflect.DeepEqual(f.Amenities, []string{"toilet", "bicycle_parking", "dog_area"}) ||
		f.MinScore == nil || *f.MinScore != 0 {
		t.Fatalf("bad parse: %+v", f)
	}
	if f, _ = ParseFilter("", "", "100"); *f.MinScore != 100 {
		t.Fatal("minScore 100 must be accepted")
	}
	if n := (Filter{}).Normalized(); n.Kinds == nil || n.Amenities == nil {
		t.Fatal("Normalized must give non-nil slices")
	}
}
