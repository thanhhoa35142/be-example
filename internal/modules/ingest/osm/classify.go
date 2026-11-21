package osm

import "strings"

type Role string

const (
	RoleCandidate Role = "candidate"
	RoleAmenity   Role = "amenity"
	RoleExcluded  Role = "excluded"
)

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

type Classified struct {
	Role    Role
	Kind    string
	Amenity AmenityType
	Name    string
	Names   map[string]string
	Attrs   map[string]string
	Quality int
	Exclude string
}

type Classifier struct {
	Policy QualityPolicy
}

type QualityPolicy struct {
	Named         int
	HasArea       int
	TableEvidence int

	PublicAccess int
	Wikidata     int

	AreaLargeHa, AreaLarge   float64
	AreaMediumHa, AreaMedium float64

	AreaTinyHa, AreaTinyPenalty float64

	AmenityPerType, AmenityMaxTypes int
}

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

func spotKind(tags map[string]string) string {
	switch {
	case tags["leisure"] == "park":
		return "park"
	case tags["natural"] == "beach":
		return "beach"
	// tourism=camp_site is not a picnic spot: campsites are mostly paid,
	// bookable or customers-only, and are chosen for overnight stays. They
	// are not fetched at all (osmium/overpass filters); a camping kind would
	// need its own scoring (overnight window, no darkness cap) and data.
	case tags["tourism"] == "picnic_site":
		return "site"
	case tags["leisure"] == "picnic_table":
		return "table"
	}
	return ""
}

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

// AttrKeys are the OSM tags copied verbatim into t_spots.attrs. Derived
// environment facts (waterDistanceM, waterKind, waterside, treeCoverPct,
// protected, protectedFee, protectedOperator) are written by the env step
// and are never in this list.
var AttrKeys = []string{
	"access", "fee", "reservation", "nudism", "supervised",
	"capacity", "description", "wikidata", "website",
	"operator", "protection_title",
}

func collectAttrs(tags map[string]string) map[string]string {
	attrs := map[string]string{}
	for _, k := range AttrKeys {
		if v, ok := tags[k]; ok {
			attrs[k] = v
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// Environment layer names (exclude_reason of the raw rows).
const (
	LayerWater     = "layer_water"
	LayerWood      = "layer_wood"
	LayerCampsite  = "layer_campsite"
	LayerProtected = "layer_protected"
)

// Water kinds stored in t_ingest_features.kind for LayerWater rows. "lake"
// is demoted to "pond" in SQL when the polygon is under PondMaxHa.
const (
	WaterSea   = "sea"
	WaterRiver = "river"
	WaterCanal = "canal"
	WaterLake  = "lake"
	WaterPond  = "pond"
)

// waterIgnored are natural=water subtypes that are not places to sit by:
// man-made basins and ornamental or technical water.
var waterIgnored = map[string]bool{
	"fountain": true, "reflecting_pool": true, "swimming_pool": true,
	"wastewater": true, "basin": true, "ditch": true, "drain": true,
	"stream_pool": true, "lock": true,
}

// layerOf maps tags to an environment layer and (for water) its kind.
// Priority when a feature carries several: campsite, water, protected, wood.
func layerOf(tags map[string]string) (layer, kind string) {
	if t := tags["tourism"]; t == "camp_site" || t == "caravan_site" {
		return LayerCampsite, ""
	}
	if k := waterKind(tags); k != "" {
		return LayerWater, k
	}
	if tags["boundary"] == "protected_area" || tags["leisure"] == "nature_reserve" {
		return LayerProtected, ""
	}
	if tags["natural"] == "wood" || tags["landuse"] == "forest" {
		return LayerWood, ""
	}
	return "", ""
}

func waterKind(tags map[string]string) string {
	// Hidden or covered water: culverts and pipes are not visible from a
	// picnic blanket.
	if tags["covered"] == "yes" || tags["location"] == "underground" ||
		tags["location"] == "underwater" || tags["intermittent"] == "yes" {
		return ""
	}
	switch tags["tunnel"] {
	case "yes", "culvert", "flooded", "building_passage":
		return ""
	}
	switch {
	case tags["natural"] == "coastline":
		return WaterSea
	case tags["waterway"] == "river" || tags["waterway"] == "riverbank":
		return WaterRiver
	case tags["waterway"] == "canal":
		return WaterCanal
	case tags["natural"] == "water":
		switch w := tags["water"]; {
		case waterIgnored[w]:
			return ""
		case w == "river" || w == "riverbank":
			return WaterRiver
		case w == "canal":
			return WaterCanal
		case w == "pond":
			return WaterPond
		case w == "stream":
			return ""
		default:
			return WaterLake
		}
	}
	return ""
}

func isArea(geojson []byte) bool {
	prefix := string(geojson)
	return strings.Contains(prefix, `"Polygon"`) || strings.Contains(prefix, `"MultiPolygon"`)
}

func (c Classifier) Classify(f RawFeature) Classified {
	qp := c.Policy
	if qp == (QualityPolicy{}) {
		qp = DefaultQualityPolicy
	}

	// Environment layers (water, wood, campsite, protected area) are not
	// spots: they are stored as excluded rows with exclude_reason
	// "layer_*" and read by the per-spot environment queries. They are
	// checked before hardExcluded because a private campsite is still a
	// campsite, but only for features that are not spots/amenities.
	if amenityType(f.Tags) == "" && spotKind(f.Tags) == "" {
		if layer, kind := layerOf(f.Tags); layer != "" {
			return Classified{Role: RoleExcluded, Exclude: layer, Kind: kind}
		}
	}

	if reason := hardExcluded(f.Tags); reason != "" {
		return Classified{Role: RoleExcluded, Exclude: reason}
	}

	if f.Tags["leisure"] == "garden" {
		return Classified{Role: RoleExcluded, Exclude: "garden"}
	}

	if f.Tags["natural"] == "beach" &&
		(f.Tags["nudism"] == "yes" || f.Tags["nudism"] == "designated") {
		return Classified{Role: RoleExcluded, Exclude: "nudist_beach"}
	}

	if a := amenityType(f.Tags); a != "" {
		return Classified{Role: RoleAmenity, Amenity: a}
	}

	kind := spotKind(f.Tags)
	if kind == "" {

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
