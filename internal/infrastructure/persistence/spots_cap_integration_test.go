package persistence_test

// Integration test for the viewport cap in ListSpotsInBBox: the top-N by
// score at the requested hour, deterministic tie-break, uncapped total.
// Requires TEST_DATABASE_URL (scratch DB); skipped otherwise.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

func TestListSpotsInBBoxCapsToTopScores(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM t_spots WHERE city_pack_id=$1`, pack)
		_, _ = pool.Exec(context.Background(), `DELETE FROM t_city_packs WHERE id=$1`, pack)
	})
	var cfg int16
	if err := pool.QueryRow(ctx, `SELECT min(version) FROM t_scoring_configs`).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)

	// 8 spots in a private 0.1 deg box far from other tests' data. Scores:
	// 90, 90, 80, 70, 70, 70, none, none (ties exercise the id tie-break).
	scores := []*int{p(90), p(90), p(80), p(70), p(70), p(70), nil, nil}
	ids := make([]uuid.UUID, len(scores))
	for i, sc := range scores {
		err := pool.QueryRow(ctx, `
			INSERT INTO t_spots (city_pack_id, kind, geom, country, tz, cell_id, status)
			VALUES ($1, 'park', ST_SetSRID(ST_MakePoint($2, -64.0), 4326)::geography,
			        'NL', 'Europe/Amsterdam', $3, 'pending')
			RETURNING id`, pack, 170.0+float64(i)*0.01, int64(900_000+i)).Scan(&ids[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE t_spots SET status='active' WHERE id=$1`, ids[i]); err != nil {
			t.Fatal(err)
		}
		if sc != nil {
			if _, err := pool.Exec(ctx, `
				INSERT INTO t_spot_scores (spot_id, ts_hour, config_version, score, reasons, confidence, computed_at)
				VALUES ($1, $2, $3, $4, '[]', 2, now())`, ids[i], hour, cfg, *sc); err != nil {
				t.Fatal(err)
			}
		}
	}

	repo := persistence.NewSpotsRepository(persistence.New(pool))
	box := spots.BBox{West: 169.99, South: -64.05, East: 170.1, North: -63.95}
	q := spots.ListQuery{BBox: &box}

	all, total, err := repo.List(ctx, q, hour, time.Time{}, time.Time{}, 0)
	if err != nil || len(all) != 8 || total != 8 {
		t.Fatalf("uncapped: n=%d total=%d err=%v", len(all), total, err)
	}

	top, total, err := repo.List(ctx, q, hour, time.Time{}, time.Time{}, 4)
	if err != nil || len(top) != 4 || total != 8 {
		t.Fatalf("capped: n=%d total=%d err=%v", len(top), total, err)
	}
	// 90, 90 (id order), 80, then the lowest-id 70.
	wantTies := func(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
		if a.String() < b.String() {
			return a, b
		}
		return b, a
	}
	first, second := wantTies(ids[0], ids[1])
	seventies := []uuid.UUID{ids[3], ids[4], ids[5]}
	lowest := seventies[0]
	for _, id := range seventies {
		if id.String() < lowest.String() {
			lowest = id
		}
	}
	want := []uuid.UUID{first, second, ids[2], lowest}
	for i, w := range want {
		if top[i].ID != w {
			t.Fatalf("rank %d: got %s want %s", i, top[i].ID, w)
		}
	}

	// Unscored spots rank last; the filter shrinks the total, not just the page.
	q.Filter = spots.Filter{MinScore: p(75)}
	got, total, err := repo.List(ctx, q, hour, time.Time{}, time.Time{}, 2)
	if err != nil || len(got) != 2 || total != 3 {
		t.Fatalf("minScore: n=%d total=%d err=%v", len(got), total, err)
	}
	// Same limit twice is stable.
	again, _, _ := repo.List(ctx, q, hour, time.Time{}, time.Time{}, 2)
	for i := range got {
		if got[i].ID != again[i].ID {
			t.Fatal(fmt.Sprintf("unstable order at %d", i))
		}
	}
}

func p(v int) *int { return &v }
