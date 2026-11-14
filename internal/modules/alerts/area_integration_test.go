package alerts_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
)

// onlyRule restricts Evaluate to one rule so the test never touches real
// rules in the dev database.
type onlyRule struct {
	alerts.Repository
	id uuid.UUID
}

func (o onlyRule) ListEnabledRules(ctx context.Context) ([]alerts.Rule, error) {
	all, err := o.Repository.ListEnabledRules(ctx)
	var out []alerts.Rule
	for _, r := range all {
		if r.ID == o.id {
			out = append(out, r)
		}
	}
	return out, err
}

type captureSender struct{ got []alerts.Delivery }

func (c *captureSender) Send(_ context.Context, d alerts.Delivery) error {
	c.got = append(c.got, d)
	return nil
}

// TestAreaRuleEvaluate creates an area rule over Amsterdam from a coarse
// lat/lon, evaluates it in a daytime window and checks the pushed spot is the
// best active spot within the radius of the cell center.
func TestAreaRuleEvaluate(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := persistence.New(pool)
	repo := persistence.NewAlertsRepo(q, pool)

	// A noon-UTC hour (13:00 Amsterdam in winter, 14:00 in summer) that has
	// scores; daytime so the darkness cap does not zero everything.
	var hour time.Time
	for i := 0; i < 7 && hour.IsZero(); i++ {
		h := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, i).Add(12 * time.Hour)
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM t_spot_scores WHERE ts_hour = $1`, h).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 && h.After(time.Now()) {
			hour = h
		}
	}
	if hour.IsZero() {
		t.Skip("no scored future noon hour in dev DB")
	}
	now := hour.Add(10 * time.Minute)

	dev := uuid.New()
	if _, err := q.UpsertDevice(ctx, persistence.UpsertDeviceParams{
		ID: dev, ApnsToken: pgtype.Text{String: "tok-" + dev.String(), Valid: true},
		ApnsEnv: pgtype.Text{String: "sandbox", Valid: true},
		Tz:      pgtype.Text{String: "Europe/Amsterdam", Valid: true},
		Prefs:   []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM t_devices WHERE id = $1`, dev) }) // cascades rules/deliveries

	svc := alerts.NewService(repo, entitlement.NewService(persistence.NewEntitlementRepo(q, pool)), nil)
	radius := 3000
	rule, err := svc.Create(ctx, dev, alerts.RuleInput{
		Area: &alerts.LatLon{Lat: 52.3612, Lon: 4.8791}, RadiusM: &radius,
		MinScore: 0, DaysMask: 127, StartLocal: "00:00", EndLocal: "23:59", TZ: "Europe/Amsterdam",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.H3Cell == nil || rule.SpotID != nil {
		t.Fatalf("expected area rule, got %+v", rule)
	}
	if _, _, ok := rule.AreaCenter(); !ok {
		t.Fatal("no area center")
	}

	// Second rule on a Free device must hit the plan limit.
	if _, err := svc.Create(ctx, dev, alerts.RuleInput{
		Area: &alerts.LatLon{Lat: 52.0, Lon: 4.3}, RadiusM: &radius,
		MinScore: 0, DaysMask: 127, StartLocal: "00:00", EndLocal: "23:59", TZ: "UTC",
	}); err == nil {
		t.Fatal("expected plan limit for second rule on free plan")
	}

	// Expected best spot, computed independently of the production query.
	lat, lon, _ := rule.AreaCenter()
	var wantID uuid.UUID
	var wantScore int
	err = pool.QueryRow(ctx, `
		SELECT s.id, ss.score FROM t_spots s
		JOIN t_spot_scores ss ON ss.spot_id = s.id AND ss.ts_hour = $1
		WHERE s.status = 'active'
		  AND ST_Distance(s.geom, ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography) <= $4
		ORDER BY ss.score DESC,
		         ST_Distance(s.geom, ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography), s.id
		LIMIT 1`, hour, lat, lon, float64(radius)).Scan(&wantID, &wantScore)
	if err != nil {
		t.Skipf("no scored active spot near Amsterdam at %s: %v", hour, err)
	}

	send := &captureSender{}
	ev := alerts.NewService(onlyRule{repo, rule.ID}, entitlement.NewService(persistence.NewEntitlementRepo(q, pool)), send)
	st, err := ev.Evaluate(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hour=%s stats=%+v best=%s score=%d", hour, st, wantID, wantScore)
	if st.Fired != 1 || len(send.got) != 1 {
		t.Fatalf("want 1 fired delivery, stats=%+v sent=%d", st, len(send.got))
	}
	d := send.got[0]
	if d.SpotID != wantID || d.Score != wantScore || d.RuleID != rule.ID {
		t.Fatalf("delivery %+v, want spot %s score %d", d, wantID, wantScore)
	}

	// Same window again: de-duplicated.
	st, err = ev.Evaluate(ctx, now.Add(5*time.Minute))
	if err != nil || st.Fired != 0 {
		t.Fatalf("second evaluate fired again: %+v err=%v", st, err)
	}
}
