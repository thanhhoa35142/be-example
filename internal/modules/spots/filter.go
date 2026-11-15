package spots

import (
	"errors"
	"strconv"
	"strings"
)

type Filter struct {
	Kinds     []string
	Amenities []string
	MinScore  *int
}

var (
	ErrInvalidFilter = errors.New("invalid filter")

	validKinds     = map[string]bool{"park": true, "beach": true, "site": true, "table": true}
	validAmenities = map[string]bool{
		"toilet": true, "water": true, "bbq": true, "playground": true,
		"shelter": true, "parking": true, "bicycle_parking": true, "dog_area": true,
	}
)

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

func (f Filter) Normalized() Filter {
	if f.Kinds == nil {
		f.Kinds = []string{}
	}
	if f.Amenities == nil {
		f.Amenities = []string{}
	}
	return f
}
