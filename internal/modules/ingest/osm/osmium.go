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
	"path"
	"path/filepath"
	"strconv"
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
	// PBFPath pins a single .osm.pbf extract (dev override). When empty,
	// each pack resolves its own extract as
	// <WorkDir>/<basename(pack.OSMExtract)>.osm.pbf — e.g.
	// "europe/netherlands-latest" → "netherlands-latest.osm.pbf".
	PBFPath string
	// WorkDir holds the .pbf extracts and cached artifacts; required when
	// PBFPath is empty, otherwise defaults to PBFPath dir.
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
	if s.PBFPath != "" {
		return filepath.Dir(s.PBFPath)
	}
	return "."
}

// pbfFor resolves the extract file for a pack: the explicit PBFPath when
// set, otherwise <WorkDir>/<basename(OSMExtract)>.osm.pbf. The file may be
// absent (deleted after Prepare for disk hygiene) — the cached artifacts
// are then used as-is.
func (s *OsmiumSource) pbfFor(p Pack) (string, error) {
	if s.PBFPath != "" {
		return s.PBFPath, nil
	}
	if p.OSMExtract == "" {
		return "", fmt.Errorf("pack %s has no osm_extract", p.Slug)
	}
	return s.pbfForExtract(p.OSMExtract), nil
}

func (s *OsmiumSource) pbfForExtract(extract string) string {
	return filepath.Join(s.workDir(), path.Base(extract)+".osm.pbf")
}

// artifactNames returns the cached filtered-pbf and export file paths for a
// raw extract path (artifacts are named after the current filter set).
func (s *OsmiumSource) artifactNames(pbf string) (filtered, export string) {
	base := strings.TrimSuffix(filepath.Base(pbf), ".osm.pbf")
	fv := filterVersion()
	wd := s.workDir()
	return filepath.Join(wd, base+".f"+fv+".filtered.osm.pbf"),
		filepath.Join(wd, base+".f"+fv+".export.geojsonseq")
}

// ArtifactsReady reports whether the extract's osmium export is cached and
// not older than the raw .pbf (when the raw file is still on disk). When
// true, Open needs neither the raw file nor osmium.
func (s *OsmiumSource) ArtifactsReady(extract string) bool {
	pbf := s.pbfForExtract(extract)
	filtered, export := s.artifactNames(pbf)
	fst, err1 := os.Stat(filtered)
	est, err2 := os.Stat(export)
	if err1 != nil || err2 != nil || est.Size() == 0 {
		return false
	}
	if pst, err := os.Stat(pbf); err == nil {
		return !fst.ModTime().Before(pst.ModTime()) && !est.ModTime().Before(fst.ModTime())
	}
	return true
}

// Prepare builds (or refreshes) the cached artifacts for one extract and
// removes artifacts of older filter versions. Callers delete the raw .pbf
// afterwards, so peak disk is ONE raw extract plus artifacts instead of all
// raw extracts at once.
func (s *OsmiumSource) Prepare(ctx context.Context, extract string) error {
	pbf := s.pbfForExtract(extract)
	if _, _, err := s.ensureArtifacts(ctx, pbf); err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(pbf), ".osm.pbf")
	keepF, keepE := s.artifactNames(pbf)
	stale, _ := filepath.Glob(filepath.Join(s.workDir(), base+".f*"))
	for _, f := range stale {
		if f != keepF && f != keepE {
			_ = os.Remove(f)
		}
	}
	return nil
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
// (so a refreshed .pbf invalidates downstream artifacts). osmium writes to
// "<name>.tmp" and the file is renamed into place only on success, so a
// killed run never poisons the cache with a truncated artifact.
func (s *OsmiumSource) ensureArtifact(ctx context.Context, out, inPath string, args func(outRel, inRel string) []string) (string, error) {
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if inSt, ierr := os.Stat(inPath); ierr != nil || !st.ModTime().Before(inSt.ModTime()) {
			return out, nil
		}
	}
	if _, err := os.Stat(inPath); err != nil {
		return "", fmt.Errorf("osmium input %s missing and no cached %s (download the extract first)", inPath, filepath.Base(out))
	}
	tmp := out + ".tmp"
	_ = os.Remove(tmp)
	if err := s.run(ctx, args(s.relPath(tmp), s.relPath(inPath))...); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
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

// coordinatesKey locates the GeoJSON coordinates array.
var coordinatesKey = []byte(`"coordinates"`)

// geoBBox computes the lon/lat extent of the first GeoJSON "coordinates"
// array in b (a geometry object, or a whole osmium feature line — osmium
// writes geometry before properties). It scans bytes directly instead of
// unmarshalling into []interface{}: the stream filter runs on every line
// of a country export and ~90% of lines are outside the pack.
func geoBBox(b []byte) (west, south, east, north float64, ok bool) {
	k := bytes.Index(b, coordinatesKey)
	if k < 0 {
		return 0, 0, 0, 0, false
	}
	b = b[k+len(coordinatesKey):]
	start := bytes.IndexByte(b, '[')
	if start < 0 || len(bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(b[:start]), []byte(":")))) != 0 {
		return 0, 0, 0, 0, false // key not followed by an array
	}
	b = b[start:]
	west, south, east, north = 180, 90, -180, -90
	depth, idx, pairs := 0, 0, 0
	var lon float64
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '[':
			depth++
			idx = 0
		case c == ']':
			depth--
			if depth == 0 {
				return west, south, east, north, pairs > 0
			}
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(b) && (b[j] == '.' || b[j] == 'e' || b[j] == 'E' || b[j] == '-' || b[j] == '+' || (b[j] >= '0' && b[j] <= '9')) {
				j++
			}
			v, err := strconv.ParseFloat(string(b[i:j]), 64)
			if err != nil {
				return 0, 0, 0, 0, false
			}
			switch idx {
			case 0:
				lon = v
			case 1:
				west, east = min(west, lon), max(east, lon)
				south, north = min(south, v), max(north, v)
				pairs++
			}
			idx++
			i = j - 1
		case c == ':' || c == '{' || c == '}':
			if depth == 0 {
				return 0, 0, 0, 0, false // malformed: key without array
			}
		}
	}
	return 0, 0, 0, 0, false
}

// intersectsPack reports whether the geometry bbox overlaps the pack
// bounds (with a small margin for boundary-straddling features).
func intersectsPack(g []byte, b Bounds) bool {
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
	asOf        time.Time
	pack        Bounds
	file        *os.File
	reader      *bufio.Reader
	parseErrors int
}

func (s *osmiumSnapshot) AsOf() time.Time { return s.asOf }

// ParseErrors reports geojsonseq lines that failed to decode — the caller
// surfaces them in stats instead of silently dropping features.
func (s *osmiumSnapshot) ParseErrors() int { return s.parseErrors }

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
				// Cheap byte-level bbox reject first; only lines that can
				// belong to the pack pay for a full JSON decode. Lines the
				// scanner cannot read fall through to the decoder (which
				// counts real parse errors).
				if len(raw) > 0 && !intersectsPack(raw, s.pack) {
					if err != nil {
						return
					}
					continue
				}
				var feat osmiumFeature
				if jerr := json.Unmarshal(raw, &feat); jerr != nil {
					s.parseErrors++
				} else if feat.Geometry != nil && intersectsPack(feat.Geometry, s.pack) {
					tags := map[string]string{}
					for k, v := range feat.Properties {
						if k == "created_by" {
							continue
						}
						tags[k] = v
					}
					if !yield(RawFeature{
						Source:   "osm",
						SourceID: NormalizeOsmiumID(feat.ID),
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

// NormalizeOsmiumID maps osmium's area ids back to the OSM object id so
// identities match Overpass ("w<id>"/"r<id>") and the LineString twin of a
// closed way: "a<2n>" → "w<n>", "a<2n+1>" → "r<n>". Other ids pass through.
func NormalizeOsmiumID(id string) string {
	if len(id) < 2 || id[0] != 'a' {
		return id
	}
	n, err := strconv.ParseInt(id[1:], 10, 64)
	if err != nil || n < 0 {
		return id
	}
	if n%2 == 0 {
		return "w" + strconv.FormatInt(n/2, 10)
	}
	return "r" + strconv.FormatInt((n-1)/2, 10)
}

// Open ensures the filtered pbf + geojsonseq export exist, then returns a
// snapshot streaming the export file for this pack's bounds.
func (s *OsmiumSource) Open(ctx context.Context, p Pack) (Snapshot, error) {
	pbf, err := s.pbfFor(p)
	if err != nil {
		return nil, err
	}
	_, exportPath, err := s.ensureArtifacts(ctx, pbf)
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

// ensureArtifacts runs tags-filter + export for one raw extract (cached).
func (s *OsmiumSource) ensureArtifacts(ctx context.Context, pbf string) (filtered, exportPath string, err error) {
	filteredName, exportName := s.artifactNames(pbf)
	filtered, err = s.ensureArtifact(ctx, filteredName, pbf, func(out, in string) []string {
		// -f pbf is required: the .tmp suffix hides the format from osmium.
		args := []string{"tags-filter", "-f", "pbf", "-o", out, "--overwrite", in}
		return append(args, filterExpressions...)
	})
	if err != nil {
		return "", "", err
	}

	exportPath, err = s.ensureArtifact(ctx, exportName, filtered, func(out, in string) []string {
		// "polygon" covers Polygon and MultiPolygon; osmium rejects the
		// multi* names in --geometry-types.
		return []string{"export", "-f", "geojsonseq", "-u", "type_id",
			"--geometry-types=point,linestring,polygon",
			"-o", out, in, "--overwrite"}
	})
	return filtered, exportPath, err
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
