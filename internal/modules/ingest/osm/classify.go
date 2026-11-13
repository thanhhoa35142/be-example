package osm

import "strings"

// Role is what a raw feature becomes in the canonical layer.
type Role string

const (
	RoleCandidate Role = "candidate" // may become a t_spots row
	RoleAmenity   Role = "amenity"   // attaches to a containing spot
	RoleExcluded  Role = "excluded"  // kept in raw layer for audit only
)

// AmenityType mirrors t_spot_amenities.type values (INGEST_STRATEGY §1).
type AmenityType string

const (
	AmenityToilet         AmenityType = "toilet"
	AmenityWater          AmenityType = "water"
	AmenityBBQ            AmenityType = "bbq"
	AmenityPlayground     AmenityType = "playground"
	AmenityDogArea        AmenityType = "dog_area"
	AmenityShelter        AmenityType = "shelter"
	AmenityParking        AmenityType = "parking"
	AmenityBicycleParking AmenityType = "bicycle_parking"
)

// Classified is the pure output of Classify: everything the planner needs,
// nothing it doesn't.
type Classified struct {
	Role    Role
	Kind    string            // spot kind when Role == RoleCandidate
	Amenity AmenityType       // when Role == RoleAmenity
	Name    string            // default name ("name" tag)
	Names   map[string]string // "name:xx" tags
	Attrs   map[string]string // access, fee, reservation, nudism, ...
	Quality int               // 0-100 from tags alone
	Exclude string            // non-empty = reason; row kept for audit
}

// Classifier maps OSM tags to roles/qualities. Pure — table-tested, no I/O.
type Classifier struct {
	Policy QualityPolicy
}

// QualityPolicy tunes the 0-100 quality score (INGEST_STRATEGY §4).
// Tag-only components are applied by Classify; geometry/neighbourhood
// components (size, amenities, child tables) by the planner via Bonus.
type QualityPolicy struct {
	Named         int // has a name
	HasArea       int // polygon/multipolygon
	TableEvidence int // picnic_table=yes or child tables (planner)
	// PublicAccess: explicit access=yes/permissive. Kept small — mappers
	// almost never tag access on public parks (AU: 0.3% of parks), so a
	// large weight only rewards regional tagging habits.
	PublicAccess int
	Wikidata     int // wikidata=* — notability signal
	// Size tiers (planner, polygon area in hectares).
	AreaLargeHa, AreaLarge   float64
	AreaMediumHa, AreaMedium float64
	// Pocket parks below AreaTinyHa lose AreaTinyPenalty points.
	AreaTinyHa, AreaTinyPenalty float64
	// AmenityPerType per distinct picnic amenity type (toilet, water, bbq,
	// playground, shelter) inside/next to the spot, up to AmenityMaxTypes.
	AmenityPerType, AmenityMaxTypes int
}

// DefaultQualityPolicy — calibrated 2026-11-13 on 39 packs (EU + AU):
// a named park polygon scores 50; it activates (>= 60) when it is either
// large (>= 5 ha), or >= 1 ha with one picnic amenity, or has tables or a
// wikidata entry. Every park on the known-places list (Vondelpark,
// Centennial Park, Kings Park, Stadtpark Wien, …) activates; pocket parks
// stay pending unless they carry real picnic evidence.
var DefaultQualityPolicy = QualityPolicy{
	Named:           30,
	HasArea:         20,
	TableEvidence:   25,
	PublicAccess:    5,
	Wikidata:        10,
	AreaLargeHa:     5,
	AreaLarge:       10,
	AreaMediumHa:    1,
	AreaMedium:      5,
	AreaTinyHa:      0.5,
	AreaTinyPenalty: 10,
	AmenityPerType:  5,
	AmenityMaxTypes: 2,
}

// Bonus is the planner-side quality delta for a candidate: polygon size
// tier (areaHa <= 0 means point geometry → no size term) and distinct
// picnic amenity types nearby. Pure; added on top of Classify's score.
func (qp QualityPolicy) Bonus(areaHa float64, amenityTypes int) int {
	b := 0
	switch {
	case areaHa <= 0:
	case areaHa >= qp.AreaLargeHa:
		b += int(qp.AreaLarge)
	case areaHa >= qp.AreaMediumHa:
		b += int(qp.AreaMedium)
	case areaHa < qp.AreaTinyHa:
		b -= int(qp.AreaTinyPenalty)
	}
	b += min(amenityTypes, qp.AmenityMaxTypes) * qp.AmenityPerType
	return b
}

var amenityTags = map[string]AmenityType{
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

// hardExcluded reports whether a feature can never become a spot.
func hardExcluded(tags map[string]string) string {
	switch tags["access"] {
	case "private", "no":
		return "access_" + tags["access"]
	}
	if tags["military"] == "yes" {
		return "military"
	}
	return ""
}

// spotKind maps candidate tags to a spot kind; "" = not a candidate.
func spotKind(tags map[string]string) string {
	switch {
	case tags["leisure"] == "park":
		return "park"
	case tags["natural"] == "beach":
		return "beach"
	case tags["tourism"] == "picnic_site" || tags["tourism"] == "camp_site":
		return "site"
	case tags["leisure"] == "picnic_table":
		return "table"
	}
	return ""
}

// amenityType maps amenity-bearing tags; "" = not an amenity.
func amenityType(tags map[string]string) AmenityType {
	for k, v := range amenityTags {
		parts := strings.SplitN(k, "=", 2)
		if tags[parts[0]] == parts[1] {
			return v
		}
	}
	return ""
}

func collectNames(tags map[string]string) (string, map[string]string) {
	name := tags["name"]
	names := map[string]string{}
	for k, v := range tags {
		if strings.HasPrefix(k, "name:") && v != "" {
			names[strings.TrimPrefix(k, "name:")] = v
		}
	}
	if len(names) == 0 {
		names = nil
	}
	return name, names
}

func collectAttrs(tags map[string]string) map[string]string {
	attrs := map[string]string{}
	for _, k := range []string{
		"access", "fee", "reservation", "nudism", "supervised",
		"capacity", "description", "wikidata", "website",
	} {
		if v, ok := tags[k]; ok {
			attrs[k] = v
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// isArea reports whether the GeoJSON payload is a polygon type — used for
// the HasArea quality component. Cheap sniff on the first bytes.
func isArea(geojson []byte) bool {
	prefix := string(geojson)
	return strings.Contains(prefix, `"Polygon"`) || strings.Contains(prefix, `"MultiPolygon"`)
}

// Classify implements the tag→role mapping of INGEST_STRATEGY §1.
func (c Classifier) Classify(f RawFeature) Classified {
	qp := c.Policy
	if qp == (QualityPolicy{}) {
		qp = DefaultQualityPolicy
	}

	if reason := hardExcluded(f.Tags); reason != "" {
		return Classified{Role: RoleExcluded, Exclude: reason}
	}
	// leisure=garden is explicitly out of scope for parks.
	if f.Tags["leisure"] == "garden" {
		return Classified{Role: RoleExcluded, Exclude: "garden"}
	}
	// Nudist beaches are excluded by default (product decision, attrs keep it).
	if f.Tags["natural"] == "beach" &&
		(f.Tags["nudism"] == "yes" || f.Tags["nudism"] == "designated") {
		return Classified{Role: RoleExcluded, Exclude: "nudist_beach"}
	}

	// Amenities attach to whatever contains them — classified before
	// candidates so a feature that is both (e.g. firepit inside a park
	// mapped on the park itself) resolves deterministically: candidate wins
	// only when it has a spot-kind tag.
	if a := amenityType(f.Tags); a != "" {
		return Classified{Role: RoleAmenity, Amenity: a}
	}

	kind := spotKind(f.Tags)
	if kind == "" {
		// Not a spot, not an amenity — still recorded as excluded for audit.
		return Classified{Role: RoleExcluded, Exclude: "unmapped_tags"}
	}

	name, names := collectNames(f.Tags)
	quality := 0
	if name != "" {
		quality += qp.Named
	}
	if isArea(f.GeoJSON) {
		quality += qp.HasArea
	}
	if f.Tags["access"] == "yes" || f.Tags["access"] == "permissive" {
		quality += qp.PublicAccess
	}
	if f.Tags["wikidata"] != "" {
		quality += qp.Wikidata
	}
	// picnic_table=yes is evidence for the parent spot (planner adds the
	// TableEvidence component when it counts child tables).
	if f.Tags["picnic_table"] == "yes" {
		quality += qp.TableEvidence
	}
	if quality > 100 {
		quality = 100
	}

	return Classified{
		Role:    RoleCandidate,
		Kind:    kind,
		Name:    name,
		Names:   names,
		Attrs:   collectAttrs(f.Tags),
		Quality: quality,
	}
}
