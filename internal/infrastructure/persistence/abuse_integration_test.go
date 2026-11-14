package persistence_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
)

func sptr(s string) *string { return &s }

func newDevice(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO t_devices (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM t_devices WHERE id=$1`, id) })
	return id
}

type deviceRow struct {
	token, env, status *string
	deleted            bool
}

func readDevice(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) deviceRow {
	t.Helper()
	var r deviceRow
	if err := pool.QueryRow(context.Background(),
		`SELECT apns_token, apns_env, push_status, deleted_at IS NOT NULL FROM t_devices WHERE id=$1`, id).
		Scan(&r.token, &r.env, &r.status, &r.deleted); err != nil {
		t.Fatal(err)
	}
	return r
}

// A reinstall that lost its Keychain registers a NEW device id with the SAME
// APNs token: the new device takes the token over, the old one is demoted
// consistently (ck_t_devices_push_token_pair) instead of a permanent 409.
func TestRegisterDeviceTakesOverAPNsToken(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewDevicesRepo(persistence.New(pool), pool)
	token := uuid.NewString() + uuid.NewString()[:4] // opaque unique string

	oldID, newID := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id = ANY($1)`, []uuid.UUID{oldID, newID})
	})
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: oldID, APNsToken: &token, APNsEnv: sptr("production")}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: newID, APNsToken: &token, APNsEnv: sptr("sandbox")}); err != nil {
		t.Fatalf("new device must take the token over, got %v", err)
	}
	prev, cur := readDevice(t, pool, oldID), readDevice(t, pool, newID)
	if prev.token != nil || prev.env != nil || prev.status == nil || *prev.status != "unregistered" {
		t.Errorf("previous holder not demoted: %+v", prev)
	}
	if cur.token == nil || *cur.token != token || *cur.status != "active" || *cur.env != "sandbox" {
		t.Errorf("new holder wrong: %+v", cur)
	}
	// The old install can re-register later without a token and stays valid.
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: oldID}); err != nil {
		t.Fatalf("re-register old: %v", err)
	}
	if r := readDevice(t, pool, oldID); r.token != nil || *r.status != "unregistered" {
		t.Errorf("old device state changed unexpectedly: %+v", r)
	}
}

// Many devices registering the same token at once: no error escapes and the
// unique index leaves exactly one holder.
func TestRegisterDeviceTokenTakeoverConcurrent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewDevicesRepo(persistence.New(pool), pool)
	token := uuid.NewString() + uuid.NewString()[:4]
	ids := make([]uuid.UUID, 8)
	for i := range ids {
		ids[i] = uuid.New()
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id = ANY($1)`, ids) })

	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Upsert(ctx, devices.RegisterInput{ID: id, APNsToken: &token})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, devices.ErrConflict) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	var holders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM t_devices WHERE apns_token=$1 AND deleted_at IS NULL`, token).Scan(&holders); err != nil || holders != 1 {
		t.Fatalf("holders = %d err=%v", holders, err)
	}
}

// Re-registering a deleted device keeps working but nothing personal returns.
func TestResurrectedDeviceStartsClean(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewDevicesRepo(persistence.New(pool), pool)
	id := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id=$1`, id) })
	token := uuid.NewString() + uuid.NewString()[:4]

	if _, err := repo.Upsert(ctx, devices.RegisterInput{
		ID: id, APNsToken: &token, APNsEnv: sptr("sandbox"), Locale: sptr("nl-NL"),
		TZ: sptr("Europe/Amsterdam"), AppVersion: sptr("1.0"), Prefs: map[string]any{"a": 1},
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Delete(ctx, id); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: id}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	var tok, env, loc, tz, ver *string
	var status, prefs string
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT apns_token, apns_env, locale, tz, app_version, push_status, prefs::text, deleted_at IS NOT NULL
		FROM t_devices WHERE id=$1`, id).Scan(&tok, &env, &loc, &tz, &ver, &status, &prefs, &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted || tok != nil || env != nil || loc != nil || tz != nil || ver != nil || status != "none" || prefs != "{}" {
		t.Fatalf("personal data resurrected: tok=%v env=%v loc=%v tz=%v ver=%v status=%s prefs=%s deleted=%v", tok, env, loc, tz, ver, status, prefs, deleted)
	}
	// And a live (non-deleted) device keeps its COALESCE-style merge.
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: id, Locale: sptr("vi-VN")}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Upsert(ctx, devices.RegisterInput{ID: id, TZ: sptr("Asia/Ho_Chi_Minh")}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT locale, tz FROM t_devices WHERE id=$1`, id).Scan(&loc, &tz); err != nil || *loc != "vi-VN" || *tz != "Asia/Ho_Chi_Minh" {
		t.Fatalf("partial updates not merged: %v %v %v", loc, tz, err)
	}
}

func seedSubscription(t *testing.T, repo *persistence.EntitlementRepo, otid string, status string, signed time.Time) {
	t.Helper()
	exp := time.Now().Add(24 * time.Hour)
	s := entitlement.Subscription{
		OriginalTransactionID: otid, ProductID: "plus", Status: status, Environment: "sandbox",
		Ownership: "purchased", ExpiresAt: &exp, LastSignedDate: signed,
	}
	if status == "revoked" {
		s.RevokedAt = &signed
	}
	if _, err := repo.UpsertSubscription(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionUpsertGuards(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewEntitlementRepo(persistence.New(pool), pool)
	otid := "test-" + uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_subscriptions WHERE original_transaction_id=$1`, otid) })
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)

	seedSubscription(t, repo, otid, "active", t0)
	exp := time.Now().Add(time.Hour)
	mk := func(status string, signed time.Time) entitlement.Subscription {
		s := entitlement.Subscription{OriginalTransactionID: otid, ProductID: "plus", Status: status, Environment: "sandbox",
			Ownership: "purchased", ExpiresAt: &exp, LastSignedDate: signed}
		if status == "revoked" {
			s.RevokedAt = &signed
		}
		return s
	}
	if applied, _ := repo.UpsertSubscription(ctx, mk("active", t0)); applied {
		t.Error("same signedDate must not re-apply")
	}
	if applied, _ := repo.UpsertSubscription(ctx, mk("expired", t0.Add(-time.Minute))); applied {
		t.Error("older signedDate must be dropped")
	}
	if applied, err := repo.UpsertSubscription(ctx, mk("revoked", t0.Add(time.Minute))); err != nil || !applied {
		t.Fatalf("newer revoke must apply: %v %v", applied, err)
	}
	// Refund is terminal: neither older nor newer non-revoked data revives it.
	if applied, _ := repo.UpsertSubscription(ctx, mk("active", t0)); applied {
		t.Error("old transaction reactivated a revoked subscription")
	}
	if applied, _ := repo.UpsertSubscription(ctx, mk("active", t0.Add(time.Hour))); applied {
		t.Error("newer non-revoked transaction un-revoked a subscription")
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM t_subscriptions WHERE original_transaction_id=$1`, otid).Scan(&status)
	if status != "revoked" {
		t.Fatalf("status = %s", status)
	}
}

func TestLinkDeviceCapIsAtomic(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewEntitlementRepo(persistence.New(pool), pool)
	otid := "test-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM t_device_subscriptions WHERE original_transaction_id=$1`, otid)
		_, _ = pool.Exec(ctx, `DELETE FROM t_subscriptions WHERE original_transaction_id=$1`, otid)
	})
	seedSubscription(t, repo, otid, "active", time.Now().Add(-time.Hour))

	const devs, max = 14, entitlement.MaxDevicesPerSubscription
	ids := make([]uuid.UUID, devs)
	for i := range ids {
		ids[i] = newDevice(t, pool)
	}
	var wg sync.WaitGroup
	var ok, limited atomic.Int32
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := repo.LinkDevice(ctx, id.String(), otid, max); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, entitlement.ErrDeviceLimit):
				limited.Add(1)
			default:
				t.Errorf("link: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != max || limited.Load() != devs-max {
		t.Fatalf("linked=%d limited=%d, want %d/%d", ok.Load(), limited.Load(), max, devs-max)
	}
	// Re-linking an existing pair at the cap is idempotent.
	var linked uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT device_id FROM t_device_subscriptions WHERE original_transaction_id=$1 LIMIT 1`, otid).Scan(&linked)
	if err := repo.LinkDevice(ctx, linked.String(), otid, max); err != nil {
		t.Fatalf("relink at cap: %v", err)
	}
	// A deleted device frees its slot.
	if ok, err := persistence.NewDevicesRepo(persistence.New(pool), pool).Delete(ctx, linked); err != nil || !ok {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := repo.LinkDevice(ctx, id.String(), otid, max); err == nil {
			return // one of the previously refused devices fit in the freed slot
		}
	}
	t.Fatal("freed slot never reusable")
}

func TestFreePlanLimitIsAtomic(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewAlertsRepo(persistence.New(pool), pool)
	spot := seedSpot(t, pool, seedCityPack(t, pool), "active")
	dev := newDevice(t, pool)

	in := alerts.RuleInput{SpotID: &spot, MinScore: 50, DaysMask: 127, StartLocal: "08:00", EndLocal: "10:00", TZ: "UTC"}
	var wg sync.WaitGroup
	var ok, limited atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := repo.InsertRuleLimited(ctx, dev, in, 8*time.Hour, 10*time.Hour, 1); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, alerts.ErrPlanLimit):
				limited.Add(1)
			default:
				t.Errorf("insert: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || limited.Load() != 9 {
		t.Fatalf("created=%d limited=%d, want 1/9", ok.Load(), limited.Load())
	}
	// Unlimited plans are not capped; unknown device is reported.
	if _, err := repo.InsertRuleLimited(ctx, dev, in, 8*time.Hour, 10*time.Hour, -1); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
	if _, err := repo.InsertRuleLimited(ctx, uuid.New(), in, 8*time.Hour, 10*time.Hour, -1); !errors.Is(err, alerts.ErrDeviceInvalid) {
		t.Fatalf("unknown device: %v", err)
	}
	// A spot that vanished maps to ErrSpotNotFound, not a 500.
	ghost := uuid.New()
	in.SpotID = &ghost
	if _, err := repo.InsertRuleLimited(ctx, dev, in, 8*time.Hour, 10*time.Hour, -1); !errors.Is(err, alerts.ErrSpotNotFound) {
		t.Fatalf("missing spot: %v", err)
	}
	if ok, _ := repo.SpotActive(ctx, spot); !ok {
		t.Fatal("seeded spot should be active")
	}
	if ok, _ := repo.SpotActive(ctx, ghost); ok {
		t.Fatal("ghost spot active")
	}
}

// Community crowd needs N DISTINCT devices; one device cannot unlock a slot
// (however many days it reports) and each device counts once in the average.
func TestCommunityCrowdNeedsDistinctDevices(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := persistence.New(pool)
	spot := seedSpot(t, pool, seedCityPack(t, pool), "active")
	repoC := persistence.NewCrowdRepo(q)

	insert := func(dev uuid.UUID, level, daysAgo int) {
		if _, err := pool.Exec(ctx, `INSERT INTO t_crowd_reports (spot_id, device_id, level, local_dow, local_hour, created_at)
			VALUES ($1,$2,$3,3,15, now() - make_interval(days => $4))`, spot, dev, level, daysAgo); err != nil {
			t.Fatal(err)
		}
	}
	slot := func(min int) (n int, avg float64, found bool) {
		slots, err := repoC.CommunityStats(ctx, min)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range slots {
			if s.SpotID == spot {
				return s.NReports, s.AvgLevel, true
			}
		}
		return 0, 0, false
	}

	spammer := newDevice(t, pool)
	for d := 1; d <= 10; d++ {
		insert(spammer, 3, d)
	}
	if _, _, found := slot(5); found {
		t.Fatal("a single device unlocked the slot with 10 reports")
	}
	for i := 0; i < 3; i++ { // 4 distinct devices in total: still below 5
		insert(newDevice(t, pool), 1, 1)
	}
	if _, _, found := slot(5); found {
		t.Fatal("4 distinct devices unlocked a 5-device slot")
	}
	insert(newDevice(t, pool), 1, 1) // 5th distinct device
	n, avg, found := slot(5)
	if !found || n != 5 {
		t.Fatalf("n_reports = %d found=%v, want 5 distinct devices", n, found)
	}
	// Per-device averaging: spammer (level 3, once) + four quiet devices (1):
	// (3+1+1+1+1)/5 = 1.4, not dominated by the spammer's 10 rows.
	if avg < 1.39 || avg > 1.41 {
		t.Fatalf("avg = %v, want 1.4", avg)
	}
}
