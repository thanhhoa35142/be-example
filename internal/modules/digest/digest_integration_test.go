package digest_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
)

type capture struct{ got []digest.Delivery }

func (c *capture) Send(_ context.Context, d digest.Delivery) error {
	c.got = append(c.got, d)
	return nil
}

// TestRunWeeklyDB runs the job against a real DB (DATABASE_URL, migrated, with
// scores for the coming weekend). It fabricates a Plus device (entitlement
// rows inserted directly — never in production code) and cleans up after.
func TestRunWeeklyDB(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close) // registered first => runs last, after fixture cleanup
	q := persistence.New(pool)

	// Pick the Thursday 18:10 (Amsterdam) preceding the coming Saturday.
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	n := time.Now().In(ams)
	days := (int(time.Saturday) - int(n.Weekday()) + 7) % 7
	sat := time.Date(n.Year(), n.Month(), n.Day()+days, 0, 0, 0, 0, ams)
	thu := time.Date(sat.Year(), sat.Month(), sat.Day()-2, 18, 10, 0, 0, ams)

	var spotID uuid.UUID
	// A spot with two adjacent daytime hours >= 60 on the weekend (any tz).
	err = pool.QueryRow(ctx, `SELECT s.id FROM t_spots s
		JOIN t_spot_scores a ON a.spot_id = s.id
		JOIN t_spot_scores b ON b.spot_id = s.id AND b.ts_hour = a.ts_hour + interval '1 hour'
		WHERE s.status='active' AND a.score >= 60 AND b.score >= 60
		  AND a.ts_hour >= $1 AND a.ts_hour < $2
		  AND extract(hour FROM a.ts_hour AT TIME ZONE s.tz) BETWEEN 8 AND 18
		  AND extract(dow FROM a.ts_hour AT TIME ZONE s.tz) IN (0, 6)
		ORDER BY a.score DESC LIMIT 1`, sat.Add(-14*time.Hour), sat.AddDate(0, 0, 2)).Scan(&spotID)
	if err != nil {
		t.Skipf("no scored Amsterdam spot for weekend %s: %v", sat.Format("2006-01-02"), err)
	}

	dev := uuid.New()
	otx := "digest-test-" + dev.String()
	mustExec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO t_devices (id, tz, locale, apns_token, apns_env, push_status) VALUES ($1::uuid,'Europe/Amsterdam','en','tok-'||$1::text,'sandbox','active')`, dev)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id=$1`, dev)
		_, _ = pool.Exec(ctx, `DELETE FROM t_subscriptions WHERE original_transaction_id=$1`, otx)
	})
	mustExec(`INSERT INTO t_subscriptions (original_transaction_id, product_id, status, environment, ownership, expires_at, last_signed_date)
		VALUES ($1,'plus.monthly','active','sandbox','purchased', now()+interval '30 days', now())`, otx)
	mustExec(`INSERT INTO t_device_subscriptions (device_id, original_transaction_id) VALUES ($1,$2)`, dev, otx)

	repo := persistence.NewDigestRepo(q)
	snd := &capture{}
	svc := digest.NewService(repo, entitlement.NewService(persistence.NewEntitlementRepo(q)), snd, slog.Default())
	if _, err := svc.Upsert(ctx, dev, []uuid.UUID{spotID}, true); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	st, err := svc.RunWeekly(ctx, thu)
	t.Logf("elapsed=%s stats=%+v err=%v", time.Since(start), st, err)
	if err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 1 || snd.got[0].DeviceID != dev || snd.got[0].SpotID != spotID {
		t.Fatalf("deliveries: %+v", snd.got)
	}
	d := snd.got[0]
	t.Logf("delivery=%+v", d)

	// Same local day again: nothing is sent.
	st, _ = svc.RunWeekly(ctx, thu.Add(30*time.Minute))
	if len(snd.got) != 1 || st.Due != 0 {
		t.Fatalf("resent: %+v %+v", st, snd.got)
	}
	got, err := repo.Get(ctx, dev)
	if err != nil || got.LastSentOn == nil || got.LastSentOn.Format("2006-01-02") != thu.Format("2006-01-02") {
		t.Fatalf("last_sent_on: %+v %v", got, err)
	}

	// Soft delete ("delete my data") removes the digest.
	if ok, err := persistence.NewDevicesRepo(q).Delete(ctx, dev); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := repo.Get(ctx, dev); err != digest.ErrNotFound {
		t.Fatalf("digest survived device delete: %v", err)
	}
}
