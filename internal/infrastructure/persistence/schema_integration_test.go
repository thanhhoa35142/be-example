package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/platform/testdb"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.DSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func expectCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation (23514), got %v", err)
	}
}

func seedCityPack(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO t_city_packs (slug, name, country, tz, boundary, status, last_import_at)
		VALUES ($1, 'Test', 'NL', 'Europe/Amsterdam',
		        ST_MakeEnvelope(4.7, 52.3, 5.1, 52.4, 4326), 'live', now())
		RETURNING id`, "test-"+uuid.NewString()[:8]).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedSpot(t *testing.T, pool *pgxpool.Pool, packID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO t_spots (city_pack_id, kind, geom, country, tz, cell_id, status)
		VALUES ($1, 'park',
		        ST_SetSRID(ST_MakePoint(4.9, 52.35), 4326)::geography,
		        'NL', 'Europe/Amsterdam', $2, $3)
		RETURNING id`, packID, time.Now().UnixNano()%1_000_000, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSpotStateMachine(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)

	id := seedSpot(t, pool, pack, "pending")
	if _, err := pool.Exec(ctx, `UPDATE t_spots SET status='active' WHERE id=$1`, id); err != nil {
		t.Fatalf("pending→active should pass: %v", err)
	}
	var transitions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM t_state_transitions WHERE entity='spot' AND entity_id=$1`,
		id.String()).Scan(&transitions); err != nil || transitions != 1 {
		t.Fatalf("expected 1 logged transition, got %d err=%v", transitions, err)
	}

	bad := seedSpot(t, pool, pack, "pending")
	_, err := pool.Exec(ctx, `UPDATE t_spots SET status='closed' WHERE id=$1`, bad)
	expectCheckViolation(t, err)

	_, err = pool.Exec(ctx, `UPDATE t_spots SET status='merged' WHERE id=$1`, id)
	expectCheckViolation(t, err)

	if _, err := pool.Exec(ctx,
		`UPDATE t_spots SET status='merged', merged_into=$2 WHERE id=$1`, id, bad); err != nil {
		t.Fatalf("active→merged should pass: %v", err)
	}

	_, err = pool.Exec(ctx, `UPDATE t_spots SET status='active', merged_into=NULL WHERE id=$1`, id)
	expectCheckViolation(t, err)
}

func TestSpotReportStateMachine(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")
	var deviceID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO t_devices DEFAULT VALUES RETURNING id`).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	var rid uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_spot_reports (spot_id, device_id, type)
		VALUES ($1, $2, 'confirm') RETURNING id`, spot, deviceID).Scan(&rid); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE t_spot_reports SET status='accepted', reviewed_at=now() WHERE id=$1`, rid); err != nil {
		t.Fatalf("pending→accepted should pass: %v", err)
	}

	_, err := pool.Exec(ctx, `UPDATE t_spot_reports SET status='pending', reviewed_at=NULL WHERE id=$1`, rid)
	expectCheckViolation(t, err)
}

func TestCityPackStateMachine(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_city_packs (slug, name, country, tz, boundary)
		VALUES ($1, 'Draft', 'NL', 'Europe/Amsterdam',
		        ST_MakeEnvelope(4.7, 52.3, 5.1, 52.4, 4326))
		RETURNING id`, "draft-"+uuid.NewString()[:8]).Scan(&id); err != nil {
		t.Fatal(err)
	}

	_, err := pool.Exec(ctx, `UPDATE t_city_packs SET status='live', last_import_at=now() WHERE id=$1`, id)
	expectCheckViolation(t, err)

	if _, err := pool.Exec(ctx, `UPDATE t_city_packs SET status='beta', last_import_at=now() WHERE id=$1`, id); err != nil {
		t.Fatalf("draft→beta should pass: %v", err)
	}
}

func TestAlertDeliveryStateMachine(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pack := seedCityPack(t, pool)
	spot := seedSpot(t, pool, pack, "active")
	var deviceID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO t_devices (apns_token, push_status, push_status_changed_at)
		 VALUES ('tok-' || $1::text, 'active', now()) RETURNING id`, uuid.NewString()).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	var ruleID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_alert_rules (device_id, spot_id, min_score, days_mask,
		                           start_local, end_local, tz)
		VALUES ($1, $2, 50, 127, '09:00', '17:00', 'Europe/Amsterdam')
		RETURNING id`, deviceID, spot).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	window := time.Now().UTC().Truncate(time.Hour)

	var claimed bool
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_alert_deliveries (rule_id, window_start, status, claimed_at)
		VALUES ($1, $2, 'claimed', now())
		ON CONFLICT DO NOTHING RETURNING true`, ruleID, window).Scan(&claimed); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_alert_deliveries (rule_id, window_start, status, claimed_at)
		VALUES ($1, $2, 'claimed', now())
		ON CONFLICT DO NOTHING RETURNING true`, ruleID, window).Scan(&n); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected no row on duplicate claim, got %v", err)
	}

	_, err := pool.Exec(ctx,
		`UPDATE t_alert_deliveries SET status='dead' WHERE rule_id=$1 AND window_start=$2`, ruleID, window)
	expectCheckViolation(t, err)
	if _, err := pool.Exec(ctx,
		`UPDATE t_alert_deliveries SET status='failed', apns_status=500, last_error='x'
		 WHERE rule_id=$1 AND window_start=$2`, ruleID, window); err != nil {
		t.Fatalf("claimed→failed should pass: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE t_alert_deliveries SET status='claimed', attempts=attempts+1, claimed_at=now()
		 WHERE rule_id=$1 AND window_start=$2 AND status='failed' AND attempts < 3`,
		ruleID, window); err != nil {
		t.Fatalf("failed→claimed should pass: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE t_alert_deliveries SET status='sent', sent_at=now(), apns_status=200
		 WHERE rule_id=$1 AND window_start=$2`, ruleID, window); err != nil {
		t.Fatalf("claimed→sent should pass: %v", err)
	}

	_, err = pool.Exec(ctx,
		`UPDATE t_alert_deliveries SET status='failed' WHERE rule_id=$1 AND window_start=$2`, ruleID, window)
	expectCheckViolation(t, err)
}

func TestJobRunAndAppleNotificationMachines(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_job_runs (job, started_at, status)
		VALUES ('test.job', now(), 'running') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE t_job_runs SET status='succeeded', finished_at=now() WHERE id=$1`, jobID); err != nil {
		t.Fatalf("running→succeeded should pass: %v", err)
	}
	_, err := pool.Exec(ctx,
		`UPDATE t_job_runs SET status='failed', finished_at=now() WHERE id=$1`, jobID)
	expectCheckViolation(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO t_job_runs (job, started_at, finished_at, status)
		 VALUES ('test.job', now(), now(), 'running')`)
	expectCheckViolation(t, err)

	var noteID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO t_apple_notifications (notification_uuid, type, signed_date, environment, payload)
		VALUES ($1, 'TEST', now(), 'sandbox', '{}'::jsonb) RETURNING notification_uuid`,
		uuid.New()).Scan(&noteID); err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx,
		`UPDATE t_apple_notifications SET status='processed' WHERE notification_uuid=$1`, noteID)
	expectCheckViolation(t, err)
	if _, err := pool.Exec(ctx,
		`UPDATE t_apple_notifications SET status='processed', processed_at=now()
		 WHERE notification_uuid=$1`, noteID); err != nil {
		t.Fatalf("received→processed should pass: %v", err)
	}
	_, err = pool.Exec(ctx,
		`UPDATE t_apple_notifications SET status='failed' WHERE notification_uuid=$1`, noteID)
	expectCheckViolation(t, err)
}

func TestDeviceAndSubscriptionChecks(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO t_devices (push_status, push_status_changed_at)
		 VALUES ('active', now())`)
	expectCheckViolation(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO t_devices (apns_token, push_status)
		 VALUES ('tok-x', 'none')`)
	expectCheckViolation(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO t_subscriptions (original_transaction_id, product_id, status,
		                             environment, ownership, last_signed_date)
		VALUES ('otid-1', 'plus.monthly', 'grace', 'production', 'purchased', now())`)
	expectCheckViolation(t, err)
}

func TestReserveProviderQuota(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := persistence.New(pool)
	provider := "test-provider-" + uuid.NewString()[:8]

	used, err := q.ReserveProviderQuota(ctx, persistence.ReserveProviderQuotaParams{
		Provider: provider, Weight: 5, QuotaLimit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if used != 5 {
		t.Fatalf("expected used=5, got %d", used)
	}

	if _, err := q.ReserveProviderQuota(ctx, persistence.ReserveProviderQuotaParams{
		Provider: provider, Weight: 5, QuotaLimit: 10,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = q.ReserveProviderQuota(ctx, persistence.ReserveProviderQuotaParams{
		Provider: provider, Weight: 1, QuotaLimit: 10,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected quota exhaustion (ErrNoRows), got %v", err)
	}

	_, err = q.ReserveProviderQuota(ctx, persistence.ReserveProviderQuotaParams{
		Provider: "test-provider-" + uuid.NewString()[:8], Weight: 11, QuotaLimit: 10,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected first-insert guard to reject, got %v", err)
	}
}
