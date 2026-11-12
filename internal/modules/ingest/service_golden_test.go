package ingest_test

// Golden-fixture integration tests for the ingest planner: canned OSM
// features stream through ingest.Service.RunPack against a real database,
// and the resulting t_spots / t_spot_sources / t_spot_amenities /
// t_ingest_features rows are asserted with SQL.
//
// Requires a migrated database; skipped when DATABASE_URL is unset.
// Dev DSN: postgres://picnic:picnic@localhost:4343/picnic?sslmode=disable
//
// Each subtest creates its own t_city_packs row (status 'draft' so the
// scheduled worker — which only lists beta/live packs — never picks it up)
// and deletes it in cleanup. t_ingest_features cascades on pack delete;
// t_spot_sources / t_spots do NOT cascade, so cleanup removes them
// explicitly (t_spot_amenities does cascade from t_spots).

import (
	"context"
	"fmt"
	"iter"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
)

// ---------------------------------------------------------------------------
// Fake osm.FeatureSource
// ---------------------------------------------------------------------------

// fixtureSource returns whatever features are currently set; tests mutate
// the slice between RunPack calls (e.g. to simulate an empty extract).
type fixtureSource struct {
	features []osm.RawFeature
}

func (f *fixtureSource) Open(context.Context, osm.Pack) (osm.Snapshot, error) {
	return fixtureSnapshot{features: f.features}, nil
}

type fixtureSnapshot struct {
	features []osm.RawFeature
}

func (fixtureSnapshot) AsOf() time.Time { return time.Now() }
func (fixtureSnapshot) Close() error    { return nil }

func (s fixtureSnapshot) Features() iter.Seq2[osm.RawFeature, error] {
	return func(yield func(osm.RawFeature, error) bool) {
		for _, f := range s.features {
			if !yield(f, nil) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Fixture geometry helpers (small GeoJSON, EPSG:4326)
// ---------------------------------------------------------------------------

func goldenPoint(lon, lat float64) []byte {
	return []byte(fmt.Sprintf(`{"type":"Point","coordinates":[%.6f,%.6f]}`, lon, lat))
}

// goldenPolygon returns an axis-aligned square of half-size d degrees
// centered on (lon, lat).
func goldenPolygon(lon, lat, d float64) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"Polygon","coordinates":[[[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f],[%.6f,%.6f]]]}`,
		lon-d, lat-d, lon+d, lat-d, lon+d, lat+d, lon-d, lat+d, lon-d, lat-d))
}

func goldenFeature(sourceID string, version int, tags map[string]string, geom []byte) osm.RawFeature {
	return osm.RawFeature{
		Source:   "osm",
		SourceID: sourceID,
		Version:  version,
		Tags:     tags,
		GeoJSON:  geom,
	}
}

// ---------------------------------------------------------------------------
// DB helpers
// ---------------------------------------------------------------------------

func goldenPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedGoldenPack inserts a boundary covering all fixture coordinates and
// registers cleanup. Status 'draft' keeps the pack invisible to the
// scheduled ingest worker (ListIngestPacks only selects beta/live).
func seedGoldenPack(t *testing.T, pool *pgxpool.Pool, slug string) osm.Pack {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO t_city_packs (slug, name, country, tz, boundary, status, last_import_at)
		VALUES ($1, $2, 'NL', 'Europe/Amsterdam',
		        ST_Multi(ST_MakeEnvelope(4.8, 52.0, 5.4, 52.5, 4326)),
		        'draft', now())
		RETURNING id`, slug, "Golden "+slug).Scan(&id)
	if err != nil {
		t.Fatalf("seed pack %s: %v", slug, err)
	}
	pack := osm.Pack{
		ID:      id,
		Slug:    slug,
		Country: "NL",
		TZ:      "Europe/Amsterdam",
		Bounds:  osm.Bounds{West: 4.8, South: 52.0, East: 5.4, North: 52.5},
	}
	t.Cleanup(func() {
		c := context.Background()
		// t_spot_sources has no cascade; t_spots cascades t_spot_amenities;
		// t_ingest_features cascades from the pack.
		_, _ = pool.Exec(c, `DELETE FROM t_spot_sources WHERE spot_id IN
			(SELECT id FROM t_spots WHERE city_pack_id = $1)`, id)
		_, _ = pool.Exec(c, `DELETE FROM t_spots WHERE city_pack_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM t_city_packs WHERE id = $1`, id)
	})
	return pack
}

func runGolden(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	src *fixtureSource, pack osm.Pack, pol osm.Policy) ingest.Stats {
	t.Helper()
	svc := &ingest.Service{
		Src:   src,
		Store: persistence.NewIngestRepo(pool),
		Pol:   pol,
	}
	st, err := svc.RunPack(ctx, pack, 0, false)
	if err != nil {
		t.Fatalf("RunPack(%s): %v", pack.Slug, err)
	}
	t.Logf("RunPack(%s) stats: %+v", pack.Slug, st)
	return st
}

func goldenCount(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

func goldenSpots(t *testing.T, pool *pgxpool.Pool, packID uuid.UUID) []struct {
	Kind, Status, StatusReason string
	Quality                    int
} {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT kind, status, COALESCE(status_reason,''), COALESCE(quality,0)
		FROM t_spots WHERE city_pack_id = $1 ORDER BY kind`, packID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		Kind, Status, StatusReason string
		Quality                    int
	}
	for rows.Next() {
		var s struct {
			Kind, Status, StatusReason string
			Quality                    int
		}
		if err := rows.Scan(&s.Kind, &s.Status, &s.StatusReason, &s.Quality); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// parkFixture builds the canonical "park absorbs children" feature set:
// a named park polygon plus a picnic table, a picnic site and a toilet
// inside it. All coordinates are relative to (lon, lat); the polygon half
// size must exceed the child offsets.
func parkFixture(slug string, lon, lat float64) []osm.RawFeature {
	return []osm.RawFeature{
		goldenFeature("w-park-"+slug, 1,
			map[string]string{"leisure": "park", "name": "Golden Park", "access": "yes"},
			goldenPolygon(lon, lat, 0.002)),
		goldenFeature("n-table-"+slug, 1,
			map[string]string{"leisure": "picnic_table", "name": "Golden Table"},
			goldenPoint(lon-0.001, lat)),
		goldenFeature("n-site-"+slug, 1,
			map[string]string{"tourism": "picnic_site"},
			goldenPoint(lon+0.001, lat)),
		goldenFeature("n-toilet-"+slug, 1,
			map[string]string{"amenity": "toilets"},
			goldenPoint(lon, lat+0.001)),
	}
}

// ---------------------------------------------------------------------------
// Golden scenarios
// ---------------------------------------------------------------------------

func TestGoldenIngest(t *testing.T) {
	pool := goldenPool(t)
	ctx := context.Background()

	t.Run("park_absorbs_children", func(t *testing.T) {
		slug := "golden-park-absorb"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: parkFixture(slug, 5.10, 52.10)}

		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})

		// 4 raw features: park/table/site are candidates, toilet is amenity.
		if st.FeaturesRead != 4 || st.Candidates != 3 {
			t.Fatalf("expected 4 features / 3 candidates, got %+v", st)
		}
		// One active park spot; the two children attach as sources.
		if st.Created != 1 || st.Activated != 1 || st.Absorbed != 2 {
			t.Fatalf("expected created=1 activated=1 absorbed=2, got %+v", st)
		}
		if st.AmenitiesLinked != 1 {
			t.Fatalf("expected amenities_linked=1, got %+v", st)
		}
		spots := goldenSpots(t, pool, pack.ID)
		if len(spots) != 1 || spots[0].Kind != "park" || spots[0].Status != "active" {
			t.Fatalf("expected single active park spot, got %+v", spots)
		}
		// Park quality: named(30)+area(20)+access(15)+table-evidence(25)=90.
		if spots[0].Quality != 90 {
			t.Fatalf("expected park quality 90, got %d", spots[0].Quality)
		}
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_spot_sources ss
			JOIN t_spots sp ON sp.id = ss.spot_id
			WHERE sp.city_pack_id = $1`, pack.ID); n != 3 {
			t.Fatalf("expected 3 spot_sources (park+2 children), got %d", n)
		}
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_spot_amenities a
			JOIN t_spots sp ON sp.id = a.spot_id
			WHERE sp.city_pack_id = $1 AND a.type = 'toilet'`, pack.ID); n != 1 {
			t.Fatalf("expected 1 toilet amenity link, got %d", n)
		}
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_ingest_features WHERE pack_id = $1`, pack.ID); n != 4 {
			t.Fatalf("expected 4 raw rows, got %d", n)
		}
	})

	t.Run("orphan_table_cluster", func(t *testing.T) {
		slug := "golden-orphan-tables"
		pack := seedGoldenPack(t, pool, slug)
		// Four picnic_table nodes within ~30 m of each other, no park
		// polygon anywhere near → DBSCAN cluster → synthetic kind=site spot.
		lon, lat := 5.20, 52.20
		src := &fixtureSource{features: []osm.RawFeature{
			goldenFeature("n-t1-"+slug, 1,
				map[string]string{"leisure": "picnic_table"}, goldenPoint(lon, lat)),
			goldenFeature("n-t2-"+slug, 1,
				map[string]string{"leisure": "picnic_table"}, goldenPoint(lon+0.0002, lat)),
			goldenFeature("n-t3-"+slug, 1,
				map[string]string{"leisure": "picnic_table"}, goldenPoint(lon, lat+0.0002)),
			goldenFeature("n-t4-"+slug, 1,
				map[string]string{"leisure": "picnic_table"}, goldenPoint(lon+0.0001, lat+0.0001)),
		}}

		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})

		if st.Candidates != 4 || st.Created != 1 || st.OrphanSites != 1 {
			t.Fatalf("expected candidates=4 created=1 orphan_sites=1, got %+v", st)
		}
		// Cluster quality = table-evidence(25) + 10*size(4) = 65 → active.
		if st.Activated != 1 {
			t.Fatalf("expected activated=1, got %+v", st)
		}
		spots := goldenSpots(t, pool, pack.ID)
		if len(spots) != 1 || spots[0].Kind != "site" || spots[0].Status != "active" {
			t.Fatalf("expected single active site spot, got %+v", spots)
		}
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_spot_sources ss
			JOIN t_spots sp ON sp.id = ss.spot_id
			WHERE sp.city_pack_id = $1`, pack.ID); n != 4 {
			t.Fatalf("expected 4 spot_sources (one per table), got %d", n)
		}
	})

	t.Run("idempotent_rerun", func(t *testing.T) {
		slug := "golden-idempotent"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: parkFixture(slug, 5.15, 52.15)}

		st1 := runGolden(t, ctx, pool, src, pack, osm.Policy{})
		if st1.Created != 1 {
			t.Fatalf("first run: expected created=1, got %+v", st1)
		}
		st2 := runGolden(t, ctx, pool, src, pack, osm.Policy{})

		// Second identical run: nothing created, the park is updated via
		// provenance, absorbed children re-attach, nothing closes.
		if st2.Created != 0 || st2.Closed != 0 {
			t.Fatalf("second run: expected created=0 closed=0, got %+v", st2)
		}
		if st2.Updated != 1 {
			t.Fatalf("second run: expected updated=1 (park only), got %+v", st2)
		}
		if st2.Absorbed != 2 {
			t.Fatalf("second run: expected absorbed=2, got %+v", st2)
		}
		spots := goldenSpots(t, pool, pack.ID)
		if len(spots) != 1 || spots[0].Status != "active" {
			t.Fatalf("expected single active spot after rerun, got %+v", spots)
		}
	})

	t.Run("below_floor_excluded", func(t *testing.T) {
		slug := "golden-below-floor"
		pack := seedGoldenPack(t, pool, slug)
		lon, lat := 5.30, 52.30
		src := &fixtureSource{features: []osm.RawFeature{
			// Unnamed point park: quality 0 — below KeepPending(35).
			goldenFeature("n-lowpt-"+slug, 1,
				map[string]string{"leisure": "park"}, goldenPoint(lon, lat)),
			// Unnamed park polygon: quality = HasArea(20) only — still
			// below the floor. Documents that area alone is not enough.
			goldenFeature("w-lowpoly-"+slug, 1,
				map[string]string{"leisure": "park"}, goldenPolygon(lon+0.01, lat, 0.002)),
		}}

		st := runGolden(t, ctx, pool, src, pack, osm.Policy{})

		if st.FeaturesRead != 2 || st.Candidates != 2 || st.Created != 0 {
			t.Fatalf("expected 2 candidates created=0, got %+v", st)
		}
		// Both stay in the raw layer only — no t_spots rows at all.
		if st.BelowFloor != 2 {
			t.Fatalf("expected below_floor=2, got %+v", st)
		}
		if n := goldenCount(t, pool,
			`SELECT count(*) FROM t_spots WHERE city_pack_id = $1`, pack.ID); n != 0 {
			t.Fatalf("expected 0 spots below floor, got %d", n)
		}
		// …but the raw rows are kept for audit with role='candidate'.
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_ingest_features
			WHERE pack_id = $1 AND role = 'candidate'`, pack.ID); n != 2 {
			t.Fatalf("expected 2 candidate raw rows, got %d", n)
		}
	})

	t.Run("missing_close", func(t *testing.T) {
		slug := "golden-missing-close"
		pack := seedGoldenPack(t, pool, slug)
		src := &fixtureSource{features: parkFixture(slug, 5.25, 52.25)}

		// Relax the close-fraction guard: this pack has a single active
		// spot, so closing it would trip MaxCloseFraction=0.15 (1 > 0.15).
		// The guard itself is covered elsewhere; here we test missing-run
		// semantics.
		pol := osm.DefaultPolicy
		pol.MaxCloseFraction = 1.0

		st1 := runGolden(t, ctx, pool, src, pack, pol)
		if st1.Created != 1 || st1.Activated != 1 {
			t.Fatalf("seed run: expected created=1 activated=1, got %+v", st1)
		}

		// Empty extract, run 1: all 3 sources (park + 2 absorbed children)
		// bump missing_runs to 1 — below CloseAfterMissing(2), stays active.
		src.features = nil
		stEmpty1 := runGolden(t, ctx, pool, src, pack, pol)
		if stEmpty1.FeaturesRead != 0 || stEmpty1.Closed != 0 {
			t.Fatalf("empty run 1: expected 0 features closed=0, got %+v", stEmpty1)
		}
		if n := goldenCount(t, pool, `
			SELECT count(*) FROM t_spot_sources ss
			JOIN t_spots sp ON sp.id = ss.spot_id
			WHERE sp.city_pack_id = $1 AND ss.missing_runs = 1`, pack.ID); n != 3 {
			t.Fatalf("after empty run 1: expected 3 sources missing_runs=1, got %d", n)
		}
		if spots := goldenSpots(t, pool, pack.ID); len(spots) != 1 || spots[0].Status != "active" {
			t.Fatalf("after empty run 1: expected still-active spot, got %+v", spots)
		}

		// Empty extract, run 2: missing_runs hits 2 >= CloseAfterMissing →
		// the spot closes with reason source_missing. Two empty runs total,
		// not three: BumpMissing increments once per run and the close
		// threshold is inclusive (>=).
		stEmpty2 := runGolden(t, ctx, pool, src, pack, pol)
		if stEmpty2.Closed != 1 {
			t.Fatalf("empty run 2: expected closed=1, got %+v", stEmpty2)
		}
		spots := goldenSpots(t, pool, pack.ID)
		if len(spots) != 1 || spots[0].Status != "closed" || spots[0].StatusReason != "source_missing" {
			t.Fatalf("expected closed/source_missing spot, got %+v", spots)
		}
	})
}
