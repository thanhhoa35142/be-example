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

func isArea(geojson []byte) bool {
	prefix := string(geojson)
	return strings.Contains(prefix, `"Polygon"`) || strings.Contains(prefix, `"MultiPolygon"`)
}

func (c Classifier) Classify(f RawFeature) Classified {
	qp := c.Policy
	if qp == (QualityPolicy{}) {
		qp = DefaultQualityPolicy
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
