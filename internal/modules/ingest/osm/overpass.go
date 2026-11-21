package osm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OverpassSource struct {
	URL    string
	Client *http.Client
}

func overpassQuery(b Bounds) string {

	bbox := fmt.Sprintf("(%f,%f,%f,%f)", b.South, b.West, b.North, b.East)
	return fmt.Sprintf(`[out:json][timeout:180];(
  nwr["leisure"~"^(park|picnic_table|playground|firepit|dog_park)$"]%s;
  nwr["natural"="beach"]%s;
  nwr["tourism"="picnic_site"]%s;
  nwr["amenity"~"^(toilets|drinking_water|bbq|shelter|parking|bicycle_parking)$"]%s;
);out geom;`, bbox, bbox, bbox, bbox)
}

type overpassElement struct {
	Type     string            `json:"type"`
	ID       int64             `json:"id"`
	Lat      float64           `json:"lat"`
	Lon      float64           `json:"lon"`
	Version  int               `json:"version"`
	Tags     map[string]string `json:"tags"`
	Geometry []ovPoint         `json:"geometry"`
	Members  []overpassMember  `json:"members"`
}

type ovPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type overpassMember struct {
	Type     string    `json:"type"`
	Role     string    `json:"role"`
	Geometry []ovPoint `json:"geometry"`
}

type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

type overpassSnapshot struct {
	asOf     time.Time
	features []RawFeature
}

func (s *overpassSnapshot) AsOf() time.Time { return s.asOf }
func (s *overpassSnapshot) Close() error    { return nil }

func (s *overpassSnapshot) Features() iter.Seq2[RawFeature, error] {
	return func(yield func(RawFeature, error) bool) {
		for _, f := range s.features {
			if !yield(f, nil) {
				return
			}
		}
	}
}

func sourceID(prefix string, id int64) string {
	return fmt.Sprintf("%s%d", prefix, id)
}

func ringGeoJSON(pts []ovPoint) []byte {
	var b bytes.Buffer
	b.WriteString(`{"type":"Polygon","coordinates":[[`)
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "[%f,%f]", p.Lon, p.Lat)
	}
	b.WriteString(`]]}`)
	return b.Bytes()
}

func lineGeoJSON(pts []ovPoint) []byte {
	var b bytes.Buffer
	b.WriteString(`{"type":"LineString","coordinates":[`)
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "[%f,%f]", p.Lon, p.Lat)
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

func pointGeoJSON(lon, lat float64) []byte {
	return []byte(fmt.Sprintf(`{"type":"Point","coordinates":[%f,%f]}`, lon, lat))
}

func closedRing(pts []ovPoint) bool {
	if len(pts) < 4 {
		return false
	}
	first, last := pts[0], pts[len(pts)-1]
	return first.Lat == last.Lat && first.Lon == last.Lon
}

func elementGeoJSON(e overpassElement) []byte {
	switch e.Type {
	case "node":
		return pointGeoJSON(e.Lon, e.Lat)
	case "way":
		if len(e.Geometry) == 0 {
			return pointGeoJSON(e.Lon, e.Lat)
		}
		if closedRing(e.Geometry) {
			return ringGeoJSON(e.Geometry)
		}
		return lineGeoJSON(e.Geometry)
	case "relation":
		// Multipolygon/boundary relations keep their real outline; the old
		// vertex-mean Point made every large park a point-only spot (no
		// area, every amenity "nearby"). Fall back to a point only when
		// the rings cannot be closed from the member geometries.
		if e.Tags["type"] == "multipolygon" || e.Tags["type"] == "boundary" {
			if g := multipolygonGeoJSON(e.Members); g != nil {
				return g
			}
		}
		var sumLat, sumLon float64
		n := 0
		for _, m := range e.Members {
			for _, p := range m.Geometry {
				sumLat += p.Lat
				sumLon += p.Lon
				n++
			}
		}
		if n == 0 {
			return nil
		}
		return pointGeoJSON(sumLon/float64(n), sumLat/float64(n))
	}
	return nil
}

func (s *OverpassSource) Open(ctx context.Context, p Pack) (Snapshot, error) {
	endpoint := s.URL
	if endpoint == "" {
		endpoint = "https://overpass-api.de/api/interpreter"
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 240 * time.Second}
	}

	form := url.Values{"data": {overpassQuery(p.Bounds)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "pickhour-ingest/0.1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("overpass request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overpass status %d", resp.StatusCode)
	}

	var out overpassResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("overpass decode: %w", err)
	}

	snap := &overpassSnapshot{asOf: time.Now().UTC()}
	for _, e := range out.Elements {
		geo := elementGeoJSON(e)
		if geo == nil {
			continue
		}
		var prefix string
		switch e.Type {
		case "node":
			prefix = "n"
		case "way":
			prefix = "w"
		case "relation":
			prefix = "r"
		}
		snap.features = append(snap.features, RawFeature{
			Source:   "osm",
			SourceID: sourceID(prefix, e.ID),
			Version:  e.Version,
			Tags:     e.Tags,
			GeoJSON:  geo,
		})
	}
	return snap, nil
}
