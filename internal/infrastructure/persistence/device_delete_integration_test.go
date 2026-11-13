package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
)

// "Delete my data": deleting a device removes its alert rules (+ deliveries),
// weekly digest, analytics events and push identifiers; the soft-deleted row remains.
func TestDeleteDeviceRemovesPersonalData(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")

	var dev uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_devices (apns_token, apns_env, push_status, attest_key_id, prefs)
		VALUES ($1, 'sandbox', 'active', 'key', '{"a":1}') RETURNING id`, "tok-"+uuid.NewString()).Scan(&dev); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id=$1`, dev) })

	var rule uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_alert_rules (device_id, spot_id, min_score, days_mask, start_local, end_local, tz)
		VALUES ($1, $2, 70, 127, '08:00', '20:00', 'Europe/Amsterdam') RETURNING id`, dev, spot).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_alert_deliveries (rule_id, window_start) VALUES ($1, now())`, rule); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_weekly_digests (device_id, spot_ids) VALUES ($1, ARRAY[$2]::uuid[])`, dev, spot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_events (device_id, name) VALUES ($1, 'app_open'), ($1, 'spot_view')`, dev); err != nil {
		t.Fatal(err)
	}

	ok, err := persistence.NewDevicesRepo(persistence.New(pool)).Delete(ctx, dev)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}

	count := func(q string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM t_alert_rules WHERE device_id=$1`, dev); n != 0 {
		t.Errorf("alert rules left: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_alert_deliveries WHERE rule_id=$1`, rule); n != 0 {
		t.Errorf("alert deliveries left: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_weekly_digests WHERE device_id=$1`, dev); n != 0 {
		t.Errorf("digest left: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_events WHERE device_id=$1`, dev); n != 0 {
		t.Errorf("events left: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_devices WHERE id=$1 AND deleted_at IS NOT NULL
		AND apns_token IS NULL AND apns_env IS NULL AND attest_key_id IS NULL
		AND push_status='none' AND prefs='{}'::jsonb`, dev); n != 1 {
		t.Errorf("device row not soft-deleted/scrubbed")
	}
	// Second delete is a no-op (404 upstream).
	if ok, _ := persistence.NewDevicesRepo(persistence.New(pool)).Delete(ctx, dev); ok {
		t.Error("second delete should report not found")
	}
}
