package persistence_test

import (
	"context"
	"testing"

	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/crowd"
)

// Batched model upsert: inserts, skips unchanged rows, updates changed ones,
// and never overwrites community rows.
func TestUpsertModelBatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")
	repo := persistence.NewCrowdRepo(persistence.New(pool))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_crowd_estimates WHERE spot_id=$1`, spot) })

	if _, err := pool.Exec(ctx, `INSERT INTO t_crowd_estimates (spot_id,dow,hour,level,source,n_reports,updated_at)
		VALUES ($1,2,10,90,'community',7,now())`, spot); err != nil {
		t.Fatal(err)
	}
	rows := []crowd.ModelEstimate{
		{SpotID: spot, Dow: 1, Hour: 9, Level: 40},
		{SpotID: spot, Dow: 1, Hour: 10, Level: 50},
		{SpotID: spot, Dow: 2, Hour: 10, Level: 10}, // community: must be kept
	}
	if n, err := repo.UpsertModelBatch(ctx, rows); err != nil || n != 2 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}
	if n, err := repo.UpsertModelBatch(ctx, rows); err != nil || n != 0 {
		t.Fatalf("unchanged: n=%d err=%v", n, err)
	}
	rows[0].Level = 41
	if n, err := repo.UpsertModelBatch(ctx, rows); err != nil || n != 1 {
		t.Fatalf("changed: n=%d err=%v", n, err)
	}
	var level int
	var source string
	if err := pool.QueryRow(ctx, `SELECT level, source FROM t_crowd_estimates WHERE spot_id=$1 AND dow=2 AND hour=10`, spot).Scan(&level, &source); err != nil ||
		level != 90 || source != "community" {
		t.Fatalf("community row overwritten: %d %s err=%v", level, source, err)
	}
}
