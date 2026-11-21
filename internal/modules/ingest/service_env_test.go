package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
)

// The fixture source delivers the environment layers (like the osmium source).
func (*fixtureSource) EnvLayers() bool { return true }

func rect(lon0, lat0, lon1, lat1 float64) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"Polygon","coordinates":[[[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f]]]}`,
		lon0, lat0, lon1, lat0, lon1, lat1, lon0, lat1, lon0, lat0))
}

func line(lon0, lat0, lon1, lat1 float64) []byte {
	return []byte(fmt.Sprintf(`{"type":"LineString","coordinates":[[%.6f,%.6f],[%.6f,%.6f]]}`, lon0, lat0, lon1, lat1))
}

func multi(polys ...[]byte) []byte {
	// wrap Polygon geojsons into a MultiPolygon
	var coords string
	for i, p := range polys {
		var g struct{ Coordinates json.RawMessage }
		_ = json.Unmarshal(p, &g)
		if i > 0 {
			coords += ","
		}
		coords += string(g.Coordinates)
	}
	return []byte(`{"type":"MultiPolygon","coordinates":[` + coords + `]}`)
}

func spotAttrs(t *testing.T, pool *pgxpool.Pool, packID any, name string) map[string]any {
	t.Helper()
	var raw []byte
	err := pool.QueryRow(context.Background(),
		`SELECT attrs FROM t_spots WHERE city_pack_id = $1 AND name = $2`, packID, name).Scan(&raw)
	if err != nil {
		t.Fatalf("attrs of %q: %v", name, err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func spotStatus(t *testing.T, pool *pgxpool.Pool, packID any, name string) (status, reason string) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(status_reason,'') FROM t_spots WHERE city_pack_id = $1 AND name = $2`,
		packID, name).Scan(&status, &reason)
	if err != nil {
		t.Fatalf("status of %q: %v", name, err)
	}
	return status, reason
}

func TestEnvFacts(t *testing.T) {
	pool := goldenPool(t)
	ctx := context.Background()

	const lon, lat = 5.10, 52.10

	t.Run("water_tree_protected_attrs", func(t *testing.T) {
		slug := "env-attrs"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: []osm.RawFeature{
			// park: 0.004 x 0.004 deg (~270 x 445 m)
			goldenFeature("w-park-"+slug, 1,
				map[string]string{"leisure": "park", "name": "Lakeside Park", "access": "yes"},
				rect(lon-0.002, lat-0.002, lon+0.002, lat+0.002)),
			// lake starting ~20 m east of the park
			goldenFeature("w-lake-"+slug, 1,
				map[string]string{"natural": "water", "water": "lake"},
				rect(lon+0.0023, lat-0.002, lon+0.006, lat+0.002)),
			// river far to the north (> 500 m)
			goldenFeature("w-river-"+slug, 1,
				map[string]string{"waterway": "river"},
				line(lon-0.01, lat+0.02, lon+0.01, lat+0.02)),
			// a culvert must be ignored (not counted as a water layer)
			goldenFeature("w-culvert-"+slug, 1,
				map[string]string{"waterway": "canal", "tunnel": "culvert"},
				line(lon-0.01, lat-0.0005, lon-0.003, lat-0.0005)),
			// forest over the western half of the park (overlapping forests
			// must not double count)
			goldenFeature("w-wood-a-"+slug, 1,
				map[string]string{"natural": "wood"},
				rect(lon-0.003, lat-0.003, lon, lat+0.003)),
			goldenFeature("w-wood-b-"+slug, 1,
				map[string]string{"landuse": "forest"},
				rect(lon-0.0025, lat-0.0025, lon-0.0005, lat+0.0025)),
			// national park polygon around everything, with operator and fee
			goldenFeature("r-prot-"+slug, 1,
				map[string]string{"boundary": "protected_area", "operator": "Staatsbosbeheer", "fee": "yes"},
				rect(lon-0.02, lat-0.02, lon+0.02, lat+0.02)),
			// a named site point outside the park, ~370 m from the lake
			goldenFeature("n-site-"+slug, 1,
				map[string]string{"tourism": "picnic_site", "name": "Point Site", "picnic_table": "yes", "fee": "no"},
				goldenPoint(lon-0.0025, lat+0.0035)),
		}}

		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})
		if st.Layers[osm.LayerWater] != 2 || st.Layers[osm.LayerWood] != 2 || st.Layers[osm.LayerProtected] != 1 {
			t.Fatalf("layer counts wrong: %+v", st.Layers)
		}
		if st.Created != 2 {
			t.Fatalf("expected 2 spots (park + site), got %+v", st)
		}

		park := spotAttrs(t, pool, pack.ID, "Lakeside Park")
		d, _ := park["waterDistanceM"].(float64)
		if park["waterKind"] != "lake" || d < 10 || d > 35 || park["waterside"] != true {
			t.Fatalf("park should be waterside by a lake ~20 m away: %v", park)
		}
		if ha, _ := park["waterBodyHa"].(float64); ha < 8 || ha > 14 {
			t.Fatalf("waterBodyHa should be the ~11 ha lake: %v", park)
		}
		tc, _ := park["treeCoverPct"].(float64)
		if tc < 45 || tc > 55 {
			t.Fatalf("expected ~50%% tree cover (overlap counted once), got %v", park["treeCoverPct"])
		}
		if park["protected"] != true || park["protectedFee"] != "yes" || park["protectedOperator"] != "Staatsbosbeheer" {
			t.Fatalf("park should inherit protected area facts: %v", park)
		}
		if park["access"] != "yes" {
			t.Fatalf("tag attrs must survive the env step: %v", park)
		}

		site := spotAttrs(t, pool, pack.ID, "Point Site")
		sd, _ := site["waterDistanceM"].(float64)
		if sd < 300 || sd > 450 || site["waterside"] != nil {
			t.Fatalf("site is ~370 m from the lake, not waterside: %v", site)
		}
		if _, has := site["treeCoverPct"]; has {
			t.Fatalf("point-only spot must not get tree cover: %v", site)
		}
		if site["fee"] != "no" {
			t.Fatalf("own fee tag must be kept: %v", site)
		}
		if st.Waterside != 1 || st.WaterNear != 2 || st.WithTreeCover != 1 || st.ProtectedSpots != 2 {
			t.Fatalf("env stats wrong: %+v", st)
		}

		// A second run with the same data changes nothing and keeps env keys.
		st2 := runGolden(t, ctx, pool, src, pack, osm.Policy{})
		if st2.Updated != 0 || st2.Created != 0 {
			t.Fatalf("rerun must be a no-op: %+v", st2)
		}
		if again := spotAttrs(t, pool, pack.ID, "Lakeside Park"); again["waterside"] != true || again["treeCoverPct"] == nil {
			t.Fatalf("env attrs lost on rerun: %v", again)
		}
	})

	t.Run("waterfront_definition", func(t *testing.T) {
		// Each park has one kind of water 20 m away.
		type tc struct {
			name  string
			tags  map[string]string
			geom  func(lon, lat float64) []byte
			kind  string
			water bool
		}
		cases := []tc{
			// 1 ha "lake": a Dutch vijver, not a waterfront
			{"small-lake", map[string]string{"natural": "water", "water": "lake"},
				func(lon, lat float64) []byte { return rect(lon+0.0023, lat, lon+0.0038, lat+0.0015) }, "pond", false},
			// 11 ha lake: a waterfront
			{"big-lake", map[string]string{"natural": "water", "water": "lake"},
				func(lon, lat float64) []byte { return rect(lon+0.0023, lat-0.002, lon+0.006, lat+0.002) }, "lake", true},
			{"river-line", map[string]string{"waterway": "river"},
				func(lon, lat float64) []byte { return line(lon+0.0023, lat-0.01, lon+0.0023, lat+0.01) }, "river", true},
			{"narrow-canal-line", map[string]string{"waterway": "canal"},
				func(lon, lat float64) []byte { return line(lon+0.0023, lat-0.01, lon+0.0023, lat+0.01) }, "canal", false},
			{"wide-canal-line", map[string]string{"waterway": "canal", "width": "25 m"},
				func(lon, lat float64) []byte { return line(lon+0.0023, lat-0.01, lon+0.0023, lat+0.01) }, "canal", true},
			{"canal-polygon-small", map[string]string{"natural": "water", "water": "canal"},
				func(lon, lat float64) []byte { return rect(lon+0.0023, lat-0.002, lon+0.0027, lat+0.002) }, "canal", false},
			{"coast", map[string]string{"natural": "coastline"},
				func(lon, lat float64) []byte { return line(lon+0.0023, lat-0.01, lon+0.0023, lat+0.01) }, "sea", true},
		}
		for i, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				slug := "env-wf-" + c.name
				pack := seedGoldenPack(t, pool, slug)
				plon := 5.10 + float64(i)*0.0 // one pack per case, same place
				src := &fixtureSource{features: []osm.RawFeature{
					goldenFeature("w-park-"+slug, 1,
						map[string]string{"leisure": "park", "name": "WF Park", "access": "yes"},
						rect(plon-0.002, lat-0.002, plon+0.002, lat+0.002)),
					goldenFeature("w-water-"+slug, 1, c.tags, c.geom(plon, lat)),
				}}
				runGolden(t, ctx, pool, src, pack, osm.Policy{})
				a := spotAttrs(t, pool, pack.ID, "WF Park")
				if a["waterKind"] != c.kind {
					t.Fatalf("waterKind = %v, want %s (%v)", a["waterKind"], c.kind, a)
				}
				if got := a["waterside"] == true; got != c.water {
					t.Fatalf("waterside = %v, want %v (%v)", got, c.water, a)
				}
			})
		}
	})

	t.Run("pond_is_not_waterside_and_tiny_lake_is_pond", func(t *testing.T) {
		slug := "env-pond"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: []osm.RawFeature{
			goldenFeature("w-park-"+slug, 1,
				map[string]string{"leisure": "park", "name": "Pond Park", "access": "yes"},
				rect(lon-0.002, lat-0.002, lon+0.002, lat+0.002)),
			// untyped water of ~0.1 ha (under the 0.2 ha pond limit) inside the park
			goldenFeature("w-puddle-"+slug, 1,
				map[string]string{"natural": "water"},
				rect(lon, lat, lon+0.0002, lat+0.0003)),
		}}
		runGolden(t, ctx, pool, src, pack, osm.Policy{})
		park := spotAttrs(t, pool, pack.ID, "Pond Park")
		if park["waterKind"] != "pond" || park["waterDistanceM"] != float64(0) {
			t.Fatalf("expected pond at 0 m, got %v", park)
		}
		if _, ok := park["waterside"]; ok {
			t.Fatalf("a pond must not make a spot waterside: %v", park)
		}
	})

	t.Run("campsite_candidates_excluded_and_existing_spot_hidden", func(t *testing.T) {
		slug := "env-camp"
		pack := seedGoldenPack(t, pool, slug)
		clon, clat := 5.20, 52.20
		fixture := func(slug string) (base []osm.RawFeature, camp osm.RawFeature) {
			site := func(id, name string, dx float64) osm.RawFeature {
				return goldenFeature(id+slug, 1,
					map[string]string{"tourism": "picnic_site", "name": name, "picnic_table": "yes"},
					goldenPoint(clon+dx, clat))
			}
			base = []osm.RawFeature{
				site("n-in-", "Fritz Camping Site", 0),
				site("n-out-", "Open Meadow Site", 0.02),
				// big park that only touches the campsite: must be kept
				goldenFeature("w-bigpark-"+slug, 1,
					map[string]string{"leisure": "park", "name": "Big Park", "access": "yes"},
					rect(clon-0.01, clat+0.0015, clon+0.01, clat+0.01)),
			}
			camp = goldenFeature("w-camp-"+slug, 1,
				map[string]string{"tourism": "camp_site", "access": "private"},
				rect(clon-0.002, clat-0.002, clon+0.002, clat+0.002))
			return base, camp
		}
		base, camp := fixture(slug)

		// Run 1: no campsite mapped yet -> both sites + park exist.
		runGolden(t, ctx, pool, &fixtureSource{features: base}, pack, osm.Policy{})
		if s, _ := spotStatus(t, pool, pack.ID, "Fritz Camping Site"); s == "hidden" {
			t.Fatalf("not hidden before the campsite exists")
		}

		// Run 2: campsite appears -> the site inside is hidden, others stay.
		st := runGolden(t, ctx, pool, &fixtureSource{features: append(append([]osm.RawFeature{}, base...), camp)}, pack, osm.Policy{})
		if st.CampsiteSkipped != 1 || st.CampsiteHidden != 1 {
			t.Fatalf("expected campsite_skipped=1 hidden=1, got %+v", st)
		}
		if s, r := spotStatus(t, pool, pack.ID, "Fritz Camping Site"); s != "hidden" || r != osm.ReasonInsideCampsite {
			t.Fatalf("site inside campsite should be hidden/inside_campsite, got %s/%s", s, r)
		}
		if s, _ := spotStatus(t, pool, pack.ID, "Open Meadow Site"); s == "hidden" {
			t.Fatalf("site outside the campsite must stay")
		}
		if s, _ := spotStatus(t, pool, pack.ID, "Big Park"); s == "hidden" {
			t.Fatalf("a park touching the campsite must stay")
		}

		// Run 3 (fresh pack state): a fresh ingest creates no spot for it.
		pack2 := seedGoldenPack(t, pool, slug+"-fresh")
		base2, camp2 := fixture(slug + "-fresh")
		st3 := runGolden(t, ctx, pool, &fixtureSource{features: append(base2, camp2)}, pack2, osm.Policy{})
		if st3.CampsiteSkipped != 1 || st3.Created != 2 {
			t.Fatalf("fresh run: expected 2 spots (meadow + park), got %+v", st3)
		}

		// Run 4: campsite removed again -> the hidden spot comes back.
		runGolden(t, ctx, pool, &fixtureSource{features: base}, pack, osm.Policy{})
		if s, _ := spotStatus(t, pool, pack.ID, "Fritz Camping Site"); s == "hidden" {
			t.Fatalf("spot should return when the campsite disappears")
		}
	})

	t.Run("park_mostly_covered_by_campsite_is_excluded", func(t *testing.T) {
		slug := "env-camp-park"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: []osm.RawFeature{
			goldenFeature("w-park-"+slug, 1,
				map[string]string{"leisure": "park", "name": "Camping Park", "access": "yes"},
				rect(lon-0.002, lat-0.002, lon+0.002, lat+0.002)),
			goldenFeature("w-camp-"+slug, 1,
				map[string]string{"tourism": "caravan_site"},
				rect(lon-0.003, lat-0.003, lon+0.003, lat+0.003)),
		}}
		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})
		if st.CampsiteSkipped != 1 || st.Created != 0 {
			t.Fatalf("park inside campsite should be skipped: %+v", st)
		}
	})
}

// A big forest polygon (thousands of vertices) is subdivided into indexed
// pieces and still gives the right share; a step timeout never fails the pack.
func TestEnvBigGeometryAndTimeoutIsolation(t *testing.T) {
	pool := goldenPool(t)
	ctx := context.Background()
	const lon, lat = 5.30, 52.30

	// A wavy 4000-vertex forest ring covering the western half of the park.
	var ring string
	const n = 2000
	pt := func(x, y float64) string { return fmt.Sprintf("[%.6f,%.6f]", x, y) }
	var pts []string
	for i := 0; i <= n; i++ { // top edge east to west, wavy
		x := lon + 0.0 - float64(i)/n*0.004
		pts = append(pts, pt(x, lat+0.003+0.0002*float64(i%2)))
	}
	for i := 0; i <= n; i++ { // bottom edge west to east
		x := lon - 0.004 + float64(i)/n*0.004
		pts = append(pts, pt(x, lat-0.003-0.0002*float64(i%2)))
	}
	pts = append(pts, pts[0])
	for i, p := range pts {
		if i > 0 {
			ring += ","
		}
		ring += p
	}
	forest := []byte(`{"type":"Polygon","coordinates":[[` + ring + `]]}`)

	build := func(slug string) *fixtureSource {
		return &fixtureSource{features: []osm.RawFeature{
			goldenFeature("w-park-"+slug, 1,
				map[string]string{"leisure": "park", "name": "Big Forest Park", "access": "yes"},
				rect(lon-0.002, lat-0.002, lon+0.002, lat+0.002)),
			goldenFeature("w-forest-"+slug, 1, map[string]string{"natural": "wood"}, forest),
		}}
	}

	t.Run("subdivided_forest_gives_right_share", func(t *testing.T) {
		slug := "env-bigforest"
		pack := seedGoldenPack(t, pool, slug)
		runGolden(t, ctx, pool, build(slug), pack, osm.Policy{})
		if n := goldenCount(t, pool, `SELECT count(*) FROM t_ingest_features WHERE pack_id=$1 AND source_id LIKE 'w-forest-%#%'`, pack.ID); n < 10 {
			t.Fatalf("forest should be split into pieces, got %d rows", n)
		}
		a := spotAttrs(t, pool, pack.ID, "Big Forest Park")
		if tc, _ := a["treeCoverPct"].(float64); tc < 45 || tc > 55 {
			t.Fatalf("expected ~50%% tree cover, got %v", a)
		}
	})

	t.Run("step_timeout_skips_env_but_keeps_the_pack", func(t *testing.T) {
		slug := "env-timeout"
		pack := seedGoldenPack(t, pool, slug)
		old := ingest.EnvStepTimeout
		ingest.EnvStepTimeout = "1ms"
		defer func() { ingest.EnvStepTimeout = old }()
		st := runGolden(t, ctx, pool, build(slug), pack, osm.Policy{})
		if len(st.EnvFailed) == 0 {
			t.Fatalf("a 1ms step timeout must be reported: %+v", st)
		}
		if st.Created != 1 {
			t.Fatalf("the pack must still import: %+v", st)
		}
		a := spotAttrs(t, pool, pack.ID, "Big Forest Park")
		if _, has := a["treeCoverPct"]; has && slices.Contains(st.EnvFailed, "tree") {
			t.Fatalf("a failed group writes nothing: %v", a)
		}
	})
}

// Missing polygons: osmium assembles a relation as a MultiPolygon, but an
// Overpass run (or a dedup that kept the point) delivers only a point for the
// same source id. An update must never drop an area that is already stored,
// and a later polygon must be recovered.
func TestGoldenPolygonRecoveryAndKeep(t *testing.T) {
	pool := goldenPool(t)
	ctx := context.Background()
	const lon, lat = 5.12, 52.12

	meta := map[string]string{"leisure": "park", "name": "Vonder Park", "type": "multipolygon", "access": "yes"}
	poly := multi(
		rect(lon-0.002, lat-0.002, lon+0.002, lat+0.002),
	)

	t.Run("point_candidate_keeps_existing_area", func(t *testing.T) {
		slug := "poly-keep"
		pack := seedGoldenPack(t, pool, slug)
		withPoly := &fixtureSource{features: []osm.RawFeature{goldenFeature("r-"+slug, 1, meta, poly)}}
		runGolden(t, ctx, pool, withPoly, pack, osm.Policy{})
		if n := goldenCount(t, pool, `SELECT count(*) FROM t_spots WHERE city_pack_id=$1 AND area IS NOT NULL`, pack.ID); n != 1 {
			t.Fatalf("first run should store the area")
		}

		// same relation, now only a point (centroid of its members)
		pointOnly := &fixtureSource{features: []osm.RawFeature{goldenFeature("r-"+slug, 2, meta, goldenPoint(lon+0.0004, lat-0.0003))}}
		st := runGolden(t, ctx, pool, pointOnly, pack, osm.Policy{})
		if st.PolygonKept != 1 {
			t.Fatalf("expected polygon_kept=1, got %+v", st)
		}
		if n := goldenCount(t, pool, `SELECT count(*) FROM t_spots WHERE city_pack_id=$1 AND area IS NOT NULL`, pack.ID); n != 1 {
			t.Fatalf("area must survive a point-only update")
		}
	})

	t.Run("polygon_recovered_after_point_only_import", func(t *testing.T) {
		slug := "poly-recover"
		pack := seedGoldenPack(t, pool, slug)
		pointOnly := &fixtureSource{features: []osm.RawFeature{goldenFeature("r-"+slug, 1, meta, goldenPoint(lon, lat))}}
		runGolden(t, ctx, pool, pointOnly, pack, osm.Policy{})
		withPoly := &fixtureSource{features: []osm.RawFeature{goldenFeature("r-"+slug, 2, meta, poly)}}
		st := runGolden(t, ctx, pool, withPoly, pack, osm.Policy{})
		if st.PolygonRecovered != 1 {
			t.Fatalf("expected polygon_recovered=1, got %+v", st)
		}
		if n := goldenCount(t, pool, `SELECT count(*) FROM t_spots WHERE city_pack_id=$1 AND area IS NOT NULL`, pack.ID); n != 1 {
			t.Fatalf("area should be recovered")
		}
	})

	t.Run("merge_prefers_polygon_over_point_for_same_id", func(t *testing.T) {
		slug := "poly-merge"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: []osm.RawFeature{
			goldenFeature("r-"+slug, 1, meta, goldenPoint(lon, lat)),
			goldenFeature("r-"+slug, 1, meta, poly),
		}}
		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})
		if st.DuplicateGeoms != 1 {
			t.Fatalf("expected duplicate_geoms=1, got %+v", st)
		}
		if n := goldenCount(t, pool, `SELECT count(*) FROM t_spots WHERE city_pack_id=$1 AND area IS NOT NULL`, pack.ID); n != 1 {
			t.Fatalf("the polygon must win the merge")
		}
	})
}

var _ = ingest.EnvWaterMaxM
