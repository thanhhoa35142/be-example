package osm

import "testing"

func feat(tags map[string]string, geojson string) RawFeature {
	return RawFeature{Source: "osm", SourceID: "n1", Tags: tags, GeoJSON: []byte(geojson)}
}

func TestClassifyCandidates(t *testing.T) {
	c := Classifier{}
	poly := `{"type":"Polygon","coordinates":[]}`

	cases := []struct {
		name     string
		tags     map[string]string
		wantRole Role
		wantKind string
		wantExcl string
	}{
		{"park", map[string]string{"leisure": "park", "name": "Vondelpark"}, RoleCandidate, "park", ""},
		{"beach", map[string]string{"natural": "beach"}, RoleCandidate, "beach", ""},
		{"picnic site", map[string]string{"tourism": "picnic_site"}, RoleCandidate, "site", ""},
		{"camp site", map[string]string{"tourism": "camp_site", "fee": "yes"}, RoleCandidate, "site", ""},
		{"lone table", map[string]string{"leisure": "picnic_table"}, RoleCandidate, "table", ""},
		{"garden excluded", map[string]string{"leisure": "garden"}, RoleExcluded, "", "garden"},
		{"private excluded", map[string]string{"leisure": "park", "access": "private"}, RoleExcluded, "", "access_private"},
		{"military excluded", map[string]string{"leisure": "park", "military": "yes"}, RoleExcluded, "", "military"},
		{"nudist beach", map[string]string{"natural": "beach", "nudism": "designated"}, RoleExcluded, "", "nudist_beach"},
		{"unmapped", map[string]string{"highway": "bench"}, RoleExcluded, "", "unmapped_tags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify(feat(tc.tags, poly))
			if got.Role != tc.wantRole {
				t.Fatalf("role = %s, want %s", got.Role, tc.wantRole)
			}
			if got.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Exclude != tc.wantExcl {
				t.Fatalf("exclude = %q, want %q", got.Exclude, tc.wantExcl)
			}
		})
	}
}

func TestClassifyAmenities(t *testing.T) {
	c := Classifier{}
	cases := map[string]AmenityType{
		"amenity=toilets":         AmenityToilet,
		"amenity=drinking_water":  AmenityWater,
		"amenity=bbq":             AmenityBBQ,
		"leisure=firepit":         AmenityBBQ,
		"leisure=playground":      AmenityPlayground,
		"leisure=dog_park":        AmenityDogArea,
		"amenity=shelter":         AmenityShelter,
		"amenity=parking":         AmenityParking,
		"amenity=bicycle_parking": AmenityBicycleParking,
	}
	for kv, want := range cases {
		var k, v string
		for i, ch := range kv {
			if ch == '=' {
				k, v = kv[:i], kv[i+1:]
			}
		}
		got := c.Classify(feat(map[string]string{k: v}, `{"type":"Point","coordinates":[0,0]}`))
		if got.Role != RoleAmenity || got.Amenity != want {
			t.Fatalf("%s → role=%s amenity=%s, want amenity %s", kv, got.Role, got.Amenity, want)
		}
	}
}

func TestClassifyQuality(t *testing.T) {
	c := Classifier{}
	poly := `{"type":"Polygon","coordinates":[]}`
	point := `{"type":"Point","coordinates":[0,0]}`

	// Named polygon park with public access: 30+20+15 = 65 → auto-activates.
	got := c.Classify(feat(map[string]string{
		"leisure": "park", "name": "X", "access": "yes",
	}, poly))
	if got.Quality != 65 {
		t.Fatalf("quality = %d, want 65", got.Quality)
	}
	// Unnamed point park: 0 → below floor.
	got = c.Classify(feat(map[string]string{"leisure": "park"}, point))
	if got.Quality != 0 {
		t.Fatalf("quality = %d, want 0", got.Quality)
	}
	// picnic_table=yes adds table evidence.
	got = c.Classify(feat(map[string]string{"leisure": "park", "picnic_table": "yes"}, point))
	if got.Quality != DefaultQualityPolicy.TableEvidence {
		t.Fatalf("quality = %d, want %d", got.Quality, DefaultQualityPolicy.TableEvidence)
	}
	// Names map collects name:xx.
	got = c.Classify(feat(map[string]string{
		"leisure": "park", "name": "X", "name:en": "X park", "name:nl": "X park nl",
	}, point))
	if got.Names["en"] != "X park" || got.Names["nl"] != "X park nl" {
		t.Fatalf("names = %v", got.Names)
	}
}
