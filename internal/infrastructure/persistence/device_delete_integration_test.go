package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
)

// "Delete my data": deleting a device removes its alert rules (+ deliveries),
// weekly digest, analytics events, subscription links and push identifiers /
// locale / tz / app version; the soft-deleted row remains. Spot reports, crowd
// reports and score feedback intentionally stay (they only feed anonymous
// aggregates and the scrubbed device row holds no personal data).
func TestDeleteDeviceRemovesPersonalData(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")

	var dev uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_devices (apns_token, apns_env, push_status, attest_key_id, prefs, locale, tz, app_version)
		VALUES ($1, 'sandbox', 'active', 'key', '{"a":1}', 'nl-NL', 'Europe/Amsterdam', '1.0') RETURNING id`, "tok-"+uuid.NewString()).Scan(&dev); err != nil {
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

	otid := "test-" + uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_subscriptions WHERE original_transaction_id=$1`, otid) })
	if _, err := pool.Exec(ctx, `INSERT INTO t_subscriptions (original_transaction_id, product_id, status, environment, ownership, last_signed_date)
		VALUES ($1, 'plus', 'active', 'sandbox', 'purchased', now())`, otid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_device_subscriptions (device_id, original_transaction_id) VALUES ($1, $2)`, dev, otid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_spot_reports (spot_id, device_id, type) VALUES ($1, $2, 'confirm')`, spot, dev); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_crowd_reports (spot_id, device_id, level, local_dow, local_hour) VALUES ($1, $2, 2, 1, 10)`, spot, dev); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO t_score_feedbacks (spot_id, device_id, ts_hour, verdict) VALUES ($1, $2, date_trunc('hour', now()), 1)`, spot, dev); err != nil {
		t.Fatal(err)
	}

	ok, err := persistence.NewDevicesRepo(persistence.New(pool), pool).Delete(ctx, dev)
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
	if n := count(`SELECT count(*) FROM t_device_subscriptions WHERE device_id=$1`, dev); n != 0 {
		t.Errorf("subscription links left: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_subscriptions WHERE original_transaction_id=$1`, otid); n != 1 {
		t.Errorf("the subscription itself (Apple-side record) must remain: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_devices WHERE id=$1 AND deleted_at IS NOT NULL
		AND apns_token IS NULL AND apns_env IS NULL AND attest_key_id IS NULL
		AND locale IS NULL AND tz IS NULL AND app_version IS NULL
		AND push_status='none' AND prefs='{}'::jsonb`, dev); n != 1 {
		t.Errorf("device row not soft-deleted/scrubbed")
	}
	// Aggregate-feeding rows stay keyed by device_id on purpose.
	if n := count(`SELECT count(*) FROM t_spot_reports WHERE device_id=$1`, dev); n != 1 {
		t.Errorf("spot report should be kept: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_crowd_reports WHERE device_id=$1`, dev); n != 1 {
		t.Errorf("crowd report should be kept: %d", n)
	}
	if n := count(`SELECT count(*) FROM t_score_feedbacks WHERE device_id=$1`, dev); n != 1 {
		t.Errorf("score feedback should be kept: %d", n)
	}
	// Second delete is a no-op (404 upstream).
	if ok, _ := persistence.NewDevicesRepo(persistence.New(pool), pool).Delete(ctx, dev); ok {
		t.Error("second delete should report not found")
	}
}
