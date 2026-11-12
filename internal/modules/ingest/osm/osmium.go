package osm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// only converts geojsonseq lines to RawFeatures.
//
// Pipeline (all heavy I/O completes inside Open, before the DB tx opens):
//  1. osmium tags-filter <extract.pbf> → <base>.filtered.osm.pbf
//     (cached per extract — the tag set is pack-independent)
//  2. osmium export -f geojsonseq -u type_id → <base>.export.geojsonseq
//     (cached per extract; pack filtering happens while streaming)
//  3. Go: parse each line, drop features whose geometry bbox does not
//     intersect the pack bounds (keeps boundary-straddling polygons)
//
// Commands run through Cmd so the binary can be a plain "osmium" or a
// wrapper (e.g. docker run … osmium) in environments without osmium-tool.
type OsmiumSource struct {
	// PBFPath is the local .osm.pbf extract.
	PBFPath string
	// WorkDir holds cached artifacts; defaults to PBFPath dir.
	WorkDir string
	// Cmd prefixes every osmium invocation and must include the binary
	// name as its last element (default ["osmium"]).
	// Docker example: []string{"docker","run","--rm","-i",
	//   "-v","/data:/data","-w","/data","pickhour-osmium","osmium"}
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

func (s *OsmiumSource) base() string {
	return strings.TrimSuffix(filepath.Base(s.PBFPath), ".osm.pbf")
}

// relPath converts an absolute path inside workDir to a relative one so it
// resolves both on the host (cmd.Dir = workDir) and inside a docker -v
// mount of the same directory.
func (s *OsmiumSource) relPath(p string) string {
	wd := s.workDir()
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

func (s *OsmiumSource) run(ctx context.Context, args ...string) error {
	argv := append(append([]string{}, s.prefix()...), args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.workDir()
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("osmium %s: %w: %s", args[0], err, tail(b, 500))
	}
	return nil
}

// filterVersion fingerprints the filter expression set so a classifier/tag
// change produces new artifact names instead of silently reusing stale ones.
func filterVersion() string {
	sum := sha256.Sum256([]byte(strings.Join(filterExpressions, "\n")))
	return hex.EncodeToString(sum[:4])
}

// ensureArtifact runs an osmium subcommand once per extract, caching the
// output file. A cached file is reused only when it is newer than its input
// (so a refreshed .pbf invalidates downstream artifacts).
func (s *OsmiumSource) ensureArtifact(ctx context.Context, name, inPath string, args func(outRel, inRel string) []string) (string, error) {
	out := filepath.Join(s.workDir(), name)
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if inSt, ierr := os.Stat(inPath); ierr != nil || !st.ModTime().Before(inSt.ModTime()) {
			return out, nil
		}
	}
	if err := s.run(ctx, args(s.relPath(out), s.relPath(inPath))...); err != nil {
		return "", err
	}
	return out, nil
}

// osmiumFeature is one geojsonseq line from `osmium export`.
type osmiumFeature struct {
	ID         string            `json:"id"` // "n123" | "w456" | "r789" | "a..." via -u type_id
	Type       string            `json:"type"`
	Geometry   json.RawMessage   `json:"geometry"`
	Properties map[string]string `json:"properties"`
}

// geoBBox computes the lon/lat extent of a GeoJSON geometry.
func geoBBox(g json.RawMessage) (west, south, east, north float64, ok bool) {
	var v struct {
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

// osmiumSnapshot streams geojsonseq lines from the cached export file.
type osmiumSnapshot struct {
	asOf   time.Time
	pack   Bounds
	file   *os.File
	reader *bufio.Reader
}

func (s *osmiumSnapshot) AsOf() time.Time { return s.asOf }

func (s *osmiumSnapshot) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func (s *osmiumSnapshot) Features() iter.Seq2[RawFeature, error] {
	return func(yield func(RawFeature, error) bool) {
		defer s.file.Close()
		for {
			raw, err := s.reader.ReadBytes('\n')
			if len(raw) > 0 {
				// geojsonseq prefixes every record with RS (0x1E).
				raw = bytes.TrimLeft(raw, "\x1e\n\r ")
				var feat osmiumFeature
				if jerr := json.Unmarshal(raw, &feat); jerr == nil && feat.Geometry != nil &&
					intersectsPack(feat.Geometry, s.pack) {
					tags := map[string]string{}
					for k, v := range feat.Properties {
						if k == "created_by" {
							continue
						}
						tags[k] = v
					}
					if !yield(RawFeature{
						Source:   "osm",
						SourceID: feat.ID,
						Version:  0, // geojsonseq has no version; content_hash covers change detection
						Tags:     tags,
						GeoJSON:  feat.Geometry,
					}, nil) {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}
}

// Open ensures the filtered pbf + geojsonseq export exist, then returns a
// snapshot streaming the export file for this pack's bounds.
func (s *OsmiumSource) Open(ctx context.Context, p Pack) (Snapshot, error) {
	fv := filterVersion()
	filtered, err := s.ensureArtifact(ctx, s.base()+".f"+fv+".filtered.osm.pbf", s.PBFPath, func(out, in string) []string {
		args := []string{"tags-filter", "-o", out, "--overwrite", in}
		return append(args, filterExpressions...)
	})
	if err != nil {
		return nil, err
	}

	exportPath, err := s.ensureArtifact(ctx, s.base()+".f"+fv+".export.geojsonseq", filtered, func(out, in string) []string {
		// "polygon" covers Polygon and MultiPolygon; osmium rejects the
		// multi* names in --geometry-types.
		return []string{"export", "-f", "geojsonseq", "-u", "type_id",
			"--geometry-types=point,linestring,polygon",
			"-o", out, in, "--overwrite"}
	})
	if err != nil {
		return nil, err
	}

	f, err := os.Open(exportPath)
	if err != nil {
		return nil, err
	}
	return &osmiumSnapshot{
		asOf:   time.Now().UTC(),
		pack:   p.Bounds,
		file:   f,
		reader: bufio.NewReaderSize(f, 1<<20),
	}, nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
