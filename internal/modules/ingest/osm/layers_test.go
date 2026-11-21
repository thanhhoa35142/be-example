package osm

import (
	"slices"
	"testing"
)

func TestClassifyEnvLayers(t *testing.T) {
	c := Classifier{}
	tests := []struct {
		name      string
		tags      map[string]string
		wantLayer string
		wantKind  string
	}{
		{"lake", map[string]string{"natural": "water", "water": "lake"}, LayerWater, WaterLake},
		{"untyped water is lake-ish", map[string]string{"natural": "water"}, LayerWater, WaterLake},
		{"pond tag", map[string]string{"natural": "water", "water": "pond"}, LayerWater, WaterPond},
		{"river area", map[string]string{"natural": "water", "water": "river"}, LayerWater, WaterRiver},
		{"riverbank", map[string]string{"waterway": "riverbank"}, LayerWater, WaterRiver},
		{"river line", map[string]string{"waterway": "river"}, LayerWater, WaterRiver},
		{"canal", map[string]string{"waterway": "canal"}, LayerWater, WaterCanal},
		{"coastline", map[string]string{"natural": "coastline"}, LayerWater, WaterSea},
		{"fountain ignored", map[string]string{"natural": "water", "water": "fountain"}, "", ""},
		{"wastewater ignored", map[string]string{"natural": "water", "water": "wastewater"}, "", ""},
		{"culvert ignored", map[string]string{"waterway": "canal", "tunnel": "culvert"}, "", ""},
		{"covered ignored", map[string]string{"waterway": "river", "covered": "yes"}, "", ""},
		{"wood", map[string]string{"natural": "wood"}, LayerWood, ""},
		{"forest", map[string]string{"landuse": "forest"}, LayerWood, ""},
		{"campsite", map[string]string{"tourism": "camp_site"}, LayerCampsite, ""},
		// private campsites are still campsites (layer wins over hardExcluded)
		{"private caravan site", map[string]string{"tourism": "caravan_site", "access": "private"}, LayerCampsite, ""},
		{"protected area", map[string]string{"boundary": "protected_area"}, LayerProtected, ""},
		{"nature reserve", map[string]string{"leisure": "nature_reserve"}, LayerProtected, ""},
		{"water beats protected", map[string]string{"natural": "water", "boundary": "protected_area"}, LayerWater, WaterLake},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(RawFeature{Tags: tt.tags, GeoJSON: []byte(`{"type":"Point"}`)})
			if tt.wantLayer == "" {
				if got.Exclude == LayerWater || got.Exclude == LayerWood || got.Exclude == LayerCampsite || got.Exclude == LayerProtected {
					t.Fatalf("expected no layer, got %+v", got)
				}
				return
			}
			if got.Role != RoleExcluded || got.Exclude != tt.wantLayer || got.Kind != tt.wantKind {
				t.Fatalf("want %s/%s, got %+v", tt.wantLayer, tt.wantKind, got)
			}
		})
	}
}

// A feature that is a spot or amenity keeps that role even when it also
// carries a layer tag (a park mapped as a forest stays a candidate).
func TestClassifyLayerDoesNotStealSpots(t *testing.T) {
	c := Classifier{}
	got := c.Classify(RawFeature{
		Tags:    map[string]string{"leisure": "park", "landuse": "forest", "name": "Bos"},
		GeoJSON: []byte(`{"type":"Polygon"}`),
	})
	if got.Role != RoleCandidate || got.Kind != "park" {
		t.Fatalf("park must stay a candidate, got %+v", got)
	}
}

func TestClassifyCollectsFeeOperatorProtection(t *testing.T) {
	got := Classifier{}.Classify(RawFeature{
		Tags: map[string]string{"leisure": "park", "name": "P", "fee": "yes",
			"operator": "Natuurmonumenten", "protection_title": "Nature reserve"},
		GeoJSON: []byte(`{"type":"Polygon"}`),
	})
	for k, want := range map[string]string{"fee": "yes", "operator": "Natuurmonumenten", "protection_title": "Nature reserve"} {
		if got.Attrs[k] != want {
			t.Fatalf("attr %s = %q, want %q", k, got.Attrs[k], want)
		}
	}
}

func TestFilterExpressionsCoverLayers(t *testing.T) {
	for _, want := range []string{
		"wr/natural=water", "w/waterway=river,canal", "w/natural=coastline",
		"wr/natural=wood", "wr/landuse=forest", "nwr/tourism=camp_site",
		"nwr/tourism=caravan_site", "wr/boundary=protected_area",
	} {
		if !slices.Contains(filterExpressions, want) {
			t.Errorf("filter missing %s", want)
		}
	}
}
