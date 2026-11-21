package osm

import (
	"bytes"
	"fmt"
)

// multipolygonGeoJSON assembles a MultiPolygon from the member ways of an
// Overpass multipolygon relation (`out geom`). Ways are stitched end to end
// into closed rings (outer ways are often split across several members);
// inner rings become holes of the outer ring that contains them. It returns
// nil when any ring cannot be closed (partial relation) so the caller can
// fall back to a point.
func multipolygonGeoJSON(members []overpassMember) []byte {
	var outerWays, innerWays [][]ovPoint
	for _, m := range members {
		if m.Type != "way" || len(m.Geometry) < 2 {
			continue
		}
		switch m.Role {
		case "inner":
			innerWays = append(innerWays, m.Geometry)
		case "outer", "":
			outerWays = append(outerWays, m.Geometry)
		}
	}
	outers, ok := stitchRings(outerWays)
	if !ok || len(outers) == 0 {
		return nil
	}
	inners, ok := stitchRings(innerWays)
	if !ok {
		inners = nil // holes are optional; an unclosed inner never blocks the outline
	}

	holes := make([][][]ovPoint, len(outers))
	for _, in := range inners {
		for i, out := range outers {
			if pointInRing(in[0], out) {
				holes[i] = append(holes[i], in)
				break
			}
		}
	}

	var b bytes.Buffer
	b.WriteString(`{"type":"MultiPolygon","coordinates":[`)
	for i, out := range outers {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("[")
		writeRing(&b, out)
		for _, h := range holes[i] {
			b.WriteByte(',')
			writeRing(&b, h)
		}
		b.WriteString("]")
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

func writeRing(b *bytes.Buffer, pts []ovPoint) {
	b.WriteByte('[')
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, "[%f,%f]", p.Lon, p.Lat)
	}
	b.WriteByte(']')
}

// stitchRings joins way segments that share end points into closed rings.
func stitchRings(ways [][]ovPoint) ([][]ovPoint, bool) {
	used := make([]bool, len(ways))
	var rings [][]ovPoint
	for i := range ways {
		if used[i] {
			continue
		}
		used[i] = true
		ring := append([]ovPoint(nil), ways[i]...)
		for ring[0] != ring[len(ring)-1] {
			joined := false
			tail := ring[len(ring)-1]
			for j := range ways {
				if used[j] {
					continue
				}
				w := ways[j]
				switch {
				case w[0] == tail:
					ring = append(ring, w[1:]...)
				case w[len(w)-1] == tail:
					for k := len(w) - 2; k >= 0; k-- {
						ring = append(ring, w[k])
					}
				default:
					continue
				}
				used[j], joined = true, true
				break
			}
			if !joined {
				return nil, false
			}
		}
		if len(ring) < 4 {
			return nil, false
		}
		rings = append(rings, ring)
	}
	return rings, true
}

func pointInRing(p ovPoint, ring []ovPoint) bool {
	in := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		a, b := ring[i], ring[j]
		if (a.Lat > p.Lat) != (b.Lat > p.Lat) &&
			p.Lon < (b.Lon-a.Lon)*(p.Lat-a.Lat)/(b.Lat-a.Lat)+a.Lon {
			in = !in
		}
	}
	return in
}
