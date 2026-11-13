package spots

import (
	"errors"
	"strconv"
	"strings"
)

// Filter is the optional server-side spot filter shared by GET /v1/spots and
// GET /v1/recommendations. The zero value filters nothing.
type Filter struct {
	Kinds     []string // keep spots whose kind is in the list (OR)
	Amenities []string // keep spots linked to every listed amenity (AND)
	MinScore  *int     // keep spots scored >= MinScore at the requested hour
}

var (
	// ErrInvalidFilter is returned by ParseFilter; handlers map it to invalid_query.
	ErrInvalidFilter = errors.New("invalid filter")

	validKinds     = map[string]bool{"park": true, "beach": true, "site": true, "table": true}
	validAmenities = map[string]bool{
		"toilet": true, "water": true, "bbq": true, "playground": true,
		"shelter": true, "parking": true, "bicycle_parking": true, "dog_area": true,
	}
)

// ParseFilter parses the raw kinds / amenities / minScore query values.
// Empty strings mean "no filter"; unknown values or a bad minScore error out.
func ParseFilter(kinds, amenities, minScore string) (Filter, error) {
	var f Filter
	var err error
	if f.Kinds, err = parseEnumList(kinds, validKinds); err != nil {
		return Filter{}, errors.New("kinds: supported values are park, beach, site, table")
	}
	if f.Amenities, err = parseEnumList(amenities, validAmenities); err != nil {
		return Filter{}, errors.New("amenities: supported values are toilet, water, bbq, playground, shelter, parking, bicycle_parking, dog_area")
	}
	if strings.TrimSpace(minScore) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(minScore))
		if err != nil || n < 0 || n > 100 {
			return Filter{}, errors.New("minScore must be an integer 0..100")
		}
		f.MinScore = &n
	}
	return f, nil
}

// parseEnumList splits a comma list, dedupes it and checks every element.
// An empty string yields an empty (non-nil) slice.
func parseEnumList(raw string, allowed map[string]bool) ([]string, error) {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if !allowed[p] {
			return nil, ErrInvalidFilter
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// Normalized returns f with non-nil slices (SQL sees empty arrays, not NULL).
func (f Filter) Normalized() Filter {
	if f.Kinds == nil {
		f.Kinds = []string{}
	}
	if f.Amenities == nil {
		f.Amenities = []string{}
	}
	return f
}
