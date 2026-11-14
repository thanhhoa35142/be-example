package http

import (
	"net/url"
	"testing"
)

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func canonBBoxOf(t *testing.T, raw string) string {
	t.Helper()
	v := mustQuery(t, "bbox="+raw)
	canon1(v, "bbox", canonBBox)
	return v.Get("bbox")
}

func TestCanonBBoxSnapsToGrid(t *testing.T) {
	// Nearby viewports must canonicalize to the same grid box.
	a := canonBBoxOf(t, "4.841,52.341,4.859,52.359")
	b := canonBBoxOf(t, "4.843,52.345,4.857,52.357")
	if a != b {
		t.Fatalf("nearby bboxes diverged: %q vs %q", a, b)
	}
	// The canonical box must CONTAIN the raw one (outward expansion).
	want := "4.84,52.34,4.86,52.36"
	if a != want {
		t.Fatalf("canonBBox=%q want %q", a, want)
	}
}

func TestCanonBBoxFallbackOnTooLarge(t *testing.T) {
	// A bbox at the 0.5° cap cannot expand without exceeding it — the raw
	// values must be kept so the service's own validation decides.
	raw := "4.001,52,4.501,52.5" // width 0.5; expansion would hit 0.52
	if got := canonBBoxOf(t, raw); got != raw {
		t.Fatalf("canonBBox=%q want raw %q", got, raw)
	}
}

func TestSpotsNormalizeWhitelistAndCanonical(t *testing.T) {
	v := mustQuery(t, "bbox=4.851,52.351,4.901,52.371&junk=1&junk=2"+
		"&when=2026-11-14T10:20:00Z&hours=012&include=area,area"+
		"&kinds=site,park,site&minScore=080&ids=&from=2026-11-14T09:05:00Z")
	SpotsNormalize(v)
	if v.Get("junk") != "" || len(v["junk"]) != 0 {
		t.Fatal("unknown params must be dropped from the key")
	}
	if got := v.Get("bbox"); got != "4.84,52.34,4.92,52.38" {
		t.Fatalf("bbox=%q", got)
	}
	if got := v.Get("when"); got != "2026-11-14T10:00:00Z" {
		t.Fatalf("when=%q want hour-truncated", got)
	}
	if got := v.Get("from"); got != "2026-11-14T09:00:00Z" {
		t.Fatalf("from=%q want hour-truncated", got)
	}
	if got := v.Get("hours"); got != "12" {
		t.Fatalf("hours=%q want canonical int", got)
	}
	if got := v.Get("include"); got != "area" {
		t.Fatalf("include=%q want deduped", got)
	}
	if got := v.Get("kinds"); got != "park,site" {
		t.Fatalf("kinds=%q want sorted deduped", got)
	}
	if got := v.Get("minScore"); got != "80" {
		t.Fatalf("minScore=%q want canonical int", got)
	}
}

func TestRecNormalizeMatchesHandlerCanon(t *testing.T) {
	v := mustQuery(t, "lat=52.3704&lon=4.8951&radius=1050&when=2026-11-14T10:20:00Z"+
		"&limit=030&kinds=beach,park&x=9")
	RecNormalize(v)
	if v.Get("x") != "" {
		t.Fatal("unknown params must be dropped")
	}
	if got := v.Get("lat"); got != "52.37" {
		t.Fatalf("lat=%q want round3", got)
	}
	if got := v.Get("lon"); got != "4.895" {
		t.Fatalf("lon=%q want round3", got)
	}
	if got := v.Get("radius"); got != "1100" {
		t.Fatalf("radius=%q want 100m bucket", got)
	}
	if got := v.Get("when"); got != "2026-11-14T10:00:00Z" {
		t.Fatalf("when=%q want hour-truncated", got)
	}
	if got := v.Get("limit"); got != "30" {
		t.Fatalf("limit=%q want canonical int", got)
	}
	if got := v.Get("kinds"); got != "beach,park" {
		t.Fatalf("kinds=%q want sorted", got)
	}
}

func TestCanonRadiusMEdges(t *testing.T) {
	for in, want := range map[float64]float64{
		0:     0,     // service default path — pass through
		-5:    -5,    // ditto
		50001: 50001, // over max — service rejects, pass through
		550:   600,
		549:   500,
		5000:  5000,
		49999: 50000, // clamps into the max bucket, still ≤ MaxRadiusM
	} {
		if got := canonRadiusM(in); got != want {
			t.Fatalf("canonRadiusM(%v)=%v want %v", in, got, want)
		}
	}
}

func TestCanonGeoBounds(t *testing.T) {
	if got := canonGeo(90)(95); got != 95 {
		t.Fatalf("out-of-range lat must pass through, got %v", got)
	}
	if got := canonGeo(90)(52.37049); got != 52.37 {
		t.Fatalf("lat=%v want round3", got)
	}
}

func TestInvalidValuesAreNotCanonicalized(t *testing.T) {
	// Unparseable values stay raw: the handler rejects them and the
	// response is never cached, so the key is never consulted.
	v := mustQuery(t, "bbox=bogus&hours=abc&when=yesterday&kinds=park,,beach")
	SpotsNormalize(v)
	if v.Get("bbox") != "bogus" || v.Get("hours") != "abc" ||
		v.Get("when") != "yesterday" || v.Get("kinds") != "park,,beach" {
		t.Fatalf("invalid params must stay raw: %v", v)
	}
}

func TestDropQueryParams(t *testing.T) {
	v := mustQuery(t, "a=1&b=2")
	DropQueryParams(v)
	if len(v) != 0 {
		t.Fatalf("params remain: %v", v)
	}
}
