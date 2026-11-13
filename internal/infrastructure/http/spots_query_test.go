package http

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

func TestParseListQuery(t *testing.T) {
	id1, id2 := uuid.NewString(), uuid.NewString()
	many := make([]string, 51)
	for i := range many {
		many[i] = uuid.NewString()
	}
	const bbox = "4.85,52.35,4.90,52.37"

	cases := []struct {
		name     string
		query    string
		wantCode string // "" = ok
	}{
		{"bbox only", "bbox=" + bbox, ""},
		{"ids only", "ids=" + id1 + "," + id2, ""},
		{"both", "bbox=" + bbox + "&ids=" + id1, "invalid_query"},
		{"neither", "when=2026-01-01T00:00:00Z", "invalid_query"},
		{"malformed bbox alone keeps invalid_bbox", "bbox=1,2,3", "invalid_bbox"},
		{"bad uuid", "ids=" + id1 + ",nope", "invalid_query"},
		{"empty ids", "ids=", "invalid_query"},
		{"empty element", "ids=" + id1 + ",", "invalid_query"},
		{"50 ids ok", "ids=" + strings.Join(many[:50], ","), ""},
		{"51 ids", "ids=" + strings.Join(many, ","), "invalid_query"},
		{"hours 1", "bbox=" + bbox + "&hours=1", ""},
		{"hours 24", "bbox=" + bbox + "&hours=24&from=2026-01-01T00:00:00Z", ""},
		{"hours 0", "bbox=" + bbox + "&hours=0", "invalid_query"},
		{"hours 25", "bbox=" + bbox + "&hours=25", "invalid_query"},
		{"hours nan", "bbox=" + bbox + "&hours=abc", "invalid_query"},
		{"hours with ids", "ids=" + id1 + "&hours=6", ""},
		{"bad from", "bbox=" + bbox + "&from=yesterday", "invalid_when"},
		{"bad when", "bbox=" + bbox + "&when=yesterday", "invalid_when"},
		{"include area", "bbox=" + bbox + "&include=area", ""},
		{"include unknown", "bbox=" + bbox + "&include=area,geometry", "invalid_query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			_, perr := parseListQuery(v)
			switch {
			case tc.wantCode == "" && perr != nil:
				t.Fatalf("unexpected problem %+v", perr)
			case tc.wantCode != "" && (perr == nil || perr.code != tc.wantCode):
				t.Fatalf("want %s, got %+v", tc.wantCode, perr)
			}
		})
	}
}

func TestParseListQueryFields(t *testing.T) {
	v, _ := url.ParseQuery("bbox=4.85,52.35,4.90,52.37&from=2026-11-13T10:30:00Z&hours=12&include=area")
	q, perr := parseListQuery(v)
	if perr != nil {
		t.Fatal(perr)
	}
	if q.BBox == nil || q.Hours != 12 || !q.IncludeArea || q.From == nil || q.When != nil {
		t.Fatalf("bad parse: %+v", q)
	}
	// when defaults to from, truncated to the hour.
	if got := scoreHour(q, time.Now()); !got.Equal(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("scoreHour = %v", got)
	}
	when := time.Date(2026, 11, 13, 15, 59, 0, 0, time.UTC)
	q.When = &when
	if got := scoreHour(q, time.Now()); got.Hour() != 15 {
		t.Fatalf("when must win over from, got %v", got)
	}
}

func TestToFeatureCollectionOptionalProps(t *testing.T) {
	items := []spots.SpotWithScore{{Spot: spots.Spot{ID: uuid.New(), Kind: spots.KindPark}}}
	has := func(fc geoJSONFeatureCollection, k string) bool {
		_, ok := fc.Features[0].Properties[k]
		return ok
	}
	fc := toFeatureCollection(items, time.Now(), false, false)
	if has(fc, "area") || has(fc, "scores") {
		t.Fatal("area/scores must be absent by default")
	}
	fc = toFeatureCollection(items, time.Now(), true, true)
	if !has(fc, "area") || fc.Features[0].Properties["area"] != nil {
		t.Fatal("area must be explicit null for point spots")
	}
	raw, ok := fc.Features[0].Properties["scores"].(json.RawMessage)
	if !ok || string(raw) != "[]" {
		t.Fatalf("scores must be [] when no hour has a score, got %v", fc.Features[0].Properties["scores"])
	}
}
