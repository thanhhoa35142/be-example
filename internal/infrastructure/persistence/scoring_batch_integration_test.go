package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
)

// Batched score upsert: nullable components round-trip, unchanged rows are
// not rewritten (computed_at stays), changed rows are.
func TestUpsertScoresBatchSkipsUnchanged(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")
	repo := persistence.NewScoringRepository(persistence.New(pool))

	var version int16
	if err := pool.QueryRow(ctx, `SELECT version FROM t_scoring_configs ORDER BY version LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	w, cr := 80, 35
	cap := "rain"
	ts := time.Now().UTC().Truncate(time.Hour).Add(1000 * time.Hour)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_spot_scores WHERE spot_id=$1`, spot) })

	rows := []scoring.ScoreWrite{
		{SpotID: spot, TSHour: ts, Score: 70, SWeather: &w, SCrowd: &cr, CapReason: &cap, Confidence: 2,
			Reasons: []scoring.Reason{}},
		{SpotID: spot, TSHour: ts.Add(time.Hour), Score: 55, Confidence: 1, Reasons: []scoring.Reason{}},
	}
	t1 := ts.Add(-10 * time.Hour)
	if n, err := repo.UpsertScores(ctx, version, rows, t1); err != nil || n != 2 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}
	var sw, sc, ssh *int16
	var cap2 *string
	if err := pool.QueryRow(ctx, `SELECT s_weather, s_crowd, cap_reason FROM t_spot_scores WHERE spot_id=$1 AND ts_hour=$2`, spot, ts).Scan(&sw, &sc, &cap2); err != nil ||
		sw == nil || *sw != 80 || sc == nil || *sc != 35 || cap2 == nil || *cap2 != "rain" {
		t.Fatalf("row 1 mismatch: %v %v %v err=%v", sw, sc, cap2, err)
	}
	if err := pool.QueryRow(ctx, `SELECT s_weather, s_shade, cap_reason FROM t_spot_scores WHERE spot_id=$1 AND ts_hour=$2`, spot, ts.Add(time.Hour)).Scan(&sw, &ssh, &cap2); err != nil ||
		sw != nil || ssh != nil || cap2 != nil {
		t.Fatalf("row 2 should have NULL components: %v %v %v err=%v", sw, ssh, cap2, err)
	}

	// Identical rewrite: nothing touched.
	t2 := t1.Add(time.Hour)
	if n, err := repo.UpsertScores(ctx, version, rows, t2); err != nil || n != 0 {
		t.Fatalf("unchanged: n=%d err=%v", n, err)
	}
	// One row changes.
	rows[1].Score = 56
	t3 := t2.Add(time.Hour)
	if n, err := repo.UpsertScores(ctx, version, rows, t3); err != nil || n != 1 {
		t.Fatalf("changed: n=%d err=%v", n, err)
	}
	var c1, c2 time.Time
	if err := pool.QueryRow(ctx, `SELECT computed_at FROM t_spot_scores WHERE spot_id=$1 AND ts_hour=$2`, spot, ts).Scan(&c1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT computed_at FROM t_spot_scores WHERE spot_id=$1 AND ts_hour=$2`, spot, ts.Add(time.Hour)).Scan(&c2); err != nil {
		t.Fatal(err)
	}
	if !c1.Equal(t1) || !c2.Equal(t3) {
		t.Errorf("computed_at: unchanged row %v want %v; changed row %v want %v", c1, t1, c2, t3)
	}
}
