package osm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OsmiumSource is the production FeatureSource: it shells out to the
// osmium-tool CLI on a local Geofabrik .pbf extract. Heavy lifting
// (multipolygon assembly, geometry export) happens in osmium; this adapter
// only streams geojsonseq and converts each feature to a RawFeature.
//
// Pipeline per pack:
//  1. osmium tags-filter <extract.pbf> → filtered.osm.pbf (cached per
//     extract — the tag set is pack-independent)
//  2. osmium export -f geojsonseq -u type_id → stdout stream
//  3. Go: parse each line, drop features whose geometry bbox does not
//     intersect the pack bounds (keeps boundary-straddling polygons),
//     emit RawFeature
//
// Commands run through Cmd so the binary can be a plain "osmium" or a
// wrapper (e.g. docker run … osmium) in environments without osmium-tool.
type OsmiumSource struct {
	// PBFPath is the local .osm.pbf extract (e.g. ./data/extracts/
	// netherlands-latest.osm.pbf).
	PBFPath string
	// WorkDir holds the cached filtered.osm.pbf; defaults to PBFPath dir.
	WorkDir string
	// Cmd prefixes every osmium invocation (default ["osmium"]).
	// Example for docker: []string{"docker","run","--rm","-i",
	//   "-v","/data:/data","-w","/data","pickhour-osmium"}
	Cmd []string
}

// filterExpressions mirrors the Classifier's tag families (INGEST_STRATEGY §1).
var filterExpressions = []string{
	"nwr/leisure=park",
	"nwr/leisure=picnic_table",
	"nwr/leisure=playground",
	"nwr/leisure=firepit",
	"nwr/leisure=dog_park",
	"nwr/leisure=garden", // kept so the classifier can record the exclusion
	"nwr/natural=beach",
	"nwr/tourism=picnic_site",
	"nwr/tourism=camp_site",
	"nwr/amenity=toilets",
	"nwr/amenity=drinking_water",
	"nwr/amenity=bbq",
	"nwr/amenity=shelter",
	"nwr/amenity=parking",
	"nwr/amenity=bicycle_parking",
	// picnic_table=yes rides on other features — match on the tag itself.
	"nwr/picnic_table=yes",
}

func (s *OsmiumSource) cmd(ctx context.Context, args ...string) *exec.Cmd {
	argv := append(append([]string{}, s.prefix()...), args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

func (s *OsmiumSource) prefix() []string {
	if len(s.Cmd) > 0 {
		return s.Cmd
	}
	return []string{"osmium"}
}

func (s *OsmiumSource) workDir() string {
	if s.WorkDir != "" {
		return s.WorkDir
	}
	return filepath.Dir(s.PBFPath)
}

// filteredPath returns the cached tags-filtered pbf for this extract.
func (s *OsmiumSource) filteredPath() string {
	base := strings.TrimSuffix(filepath.Base(s.PBFPath), ".osm.pbf")
	return filepath.Join(s.workDir(), base+".filtered.osm.pbf")
}

// ensureFiltered runs tags-filter once per extract; the output is reused
// across packs because the tag set does not depend on the boundary.
func (s *OsmiumSource) ensureFiltered(ctx context.Context) (string, error) {
	out := s.filteredPath()
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil
	}
	in := s.PBFPath
	wd := s.workDir()
	// Paths are passed workdir-relative so they resolve both on the host
	// (cmd.Dir = wd) and inside a docker -v mount of the same dir.
	if filepath.IsAbs(in) {
		if rel, err := filepath.Rel(wd, in); err == nil && !strings.HasPrefix(rel, "..") {
			in = rel
		}
	}
	outArg := out
	if filepath.IsAbs(outArg) {
		if rel, err := filepath.Rel(wd, outArg); err == nil && !strings.HasPrefix(rel, "..") {
			outArg = rel
		}
	}
	args := []string{"tags-filter", "-o", outArg, "--overwrite", in}
	args = append(args, filterExpressions...)
	cmd := s.cmd(ctx, args...)
	cmd.Dir = wd
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("osmium tags-filter: %w: %s", err, tail(b, 500))
	}
	return s.filteredPath(), nil
}

// osmiumFeature is one geojsonseq line from `osmium export`.
type osmiumFeature struct {
	ID         string            `json:"id"` // "n123" | "w456" | "r789" via -u type_id
	Type       string            `json:"type"`
	Geometry   json.RawMessage   `json:"geometry"`
	Properties map[string]string `json:"properties"`
}

// geoBBox computes the lon/lat extent of a GeoJSON geometry.
func geoBBox(g json.RawMessage) (west, south, east, north float64, ok bool) {
	var v struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(g, &v); err != nil {
		return 0, 0, 0, 0, false
	}
	west, south, east, north = 180, 90, -180, -90
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch t := x.(type) {
		case []interface{}:
			if len(t) == 2 {
				if lon, ok1 := t[0].(float64); ok1 {
					if lat, ok2 := t[1].(float64); ok2 {
						if lon < west {
							west = lon
						}
						if lon > east {
							east = lon
						}
						if lat < south {
							south = lat
						}
						if lat > north {
							north = lat
						}
					}
				}
				return
			}
			for _, e := range t {
				walk(e)
			}
		}
	}
	var coords interface{}
	if err := json.Unmarshal(v.Coordinates, &coords); err != nil {
		return 0, 0, 0, 0, false
	}
	walk(coords)
	return west, south, east, north, true
}

// intersectsPack reports whether the geometry bbox overlaps the pack
// bounds (with a small margin for boundary-straddling features).
func intersectsPack(g json.RawMessage, b Bounds) bool {
	const margin = 0.01 // ~1km — keeps straddlers, drops clearly-outside
	w, s, e, n, ok := geoBBox(g)
	if !ok {
		return true // unparseable geometry: keep, let PostGIS deal with it
	}
	return e >= b.West-margin && w <= b.East+margin &&
		n >= b.South-margin && s <= b.North+margin
}

// Open runs tags-filter (cached) then spawns `osmium export` streaming
// geojsonseq on stdout. The snapshot holds the running process; Close
// terminates it.
type osmiumSnapshot struct {
	asOf     time.Time
	pack     Bounds
	cmd      *exec.Cmd
	stdout   *bufio.Reader
	stderr   *strings.Builder
	features []RawFeature // buffered lazily on first Features() call
	done     bool
	lines    int
	badJSON  int
	outside  int
}

func (s *osmiumSnapshot) AsOf() time.Time { return s.asOf }

func (s *osmiumSnapshot) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (s *osmiumSnapshot) Features() iter.Seq2[RawFeature, error] {
	return func(yield func(RawFeature, error) bool) {
		if !s.done {
			s.done = true
			if err := s.buffer(); err != nil {
				yield(RawFeature{}, err)
				return
			}
		}
		for _, f := range s.features {
			if !yield(f, nil) {
				return
			}
		}
	}
}

// buffer drains the export stream into memory. A country extract filtered
// to picnic-relevant tags is typically tens of thousands of features —
// a few hundred MB worst case; acceptable for v1 (osmium extract path
// can pre-clip per pack later).
func (s *osmiumSnapshot) buffer() error {
	line := 0
	for {
		raw, err := s.stdout.ReadBytes('\n')
		if len(raw) > 0 {
			line++
			s.lines++
			// geojsonseq prefixes every record with RS (0x1E).
			raw = bytes.TrimLeft(raw, "\x1e\n\r ")
			var feat osmiumFeature
			if jerr := json.Unmarshal(raw, &feat); jerr != nil {
				s.badJSON++
			} else if feat.Geometry == nil {
				s.badJSON++
			} else if !intersectsPack(feat.Geometry, s.pack) {
				s.outside++
				continue
			} else {
				tags := map[string]string{}
				for k, v := range feat.Properties {
					if k == "created_by" {
						continue
					}
					tags[k] = v
				}
				s.features = append(s.features, RawFeature{
					Source:   "osm",
					SourceID: feat.ID,
					Version:  0, // geojsonseq has no version; content_hash covers change detection
					Tags:     tags,
					GeoJSON:  feat.Geometry,
				})
			}
		}
		if err != nil {
			break
		}
	}
	if werr := s.cmd.Wait(); werr != nil {
		return fmt.Errorf("osmium export: %w: %s", werr, tail([]byte(s.stderr.String()), 500))
	}
	return nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// Open prepares the filtered extract and starts the export stream.
func (s *OsmiumSource) Open(ctx context.Context, p Pack) (Snapshot, error) {
	filtered, err := s.ensureFiltered(ctx)
	if err != nil {
		return nil, err
	}

	// osmium export defaults to STDOUT when -o is omitted.
	// "polygon" covers Polygon and MultiPolygon; osmium rejects the
	// multi* names in --geometry-types.
	args := []string{"export", "-f", "geojsonseq", "-u", "type_id",
		"--geometry-types=point,linestring,polygon", filtered}
	// Make the input path workdir-relative (docker -v mounts the workdir).
	if rel, err := filepath.Rel(s.workDir(), filtered); err == nil {
		args[len(args)-1] = rel
	}
	cmd := s.cmd(ctx, args...)
	cmd.Dir = s.workDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("osmium export start: %w", err)
	}
	return &osmiumSnapshot{
		asOf:   time.Now().UTC(),
		pack:   p.Bounds,
		cmd:    cmd,
		stdout: bufio.NewReaderSize(stdout, 1<<20),
		stderr: &stderr,
	}, nil
}
