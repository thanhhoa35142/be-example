package nudge_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/nudge"
	"github.com/thanhhoa35142/be-example/internal/platform/testdb"
)

// Open-ocean point: no real spot is near it, so the only spot the nudge can
// pick here is the one this test inserts.
const (
	testLat = 0.5
	testLng = -140.0
)

type capture struct{ got []nudge.Delivery }

func (c *capture) Send(_ context.Context, d nudge.Delivery) error {
	c.got = append(c.got, d)
	return nil
}

func (c *capture) to(dev uuid.UUID) []nudge.Delivery {
	var out []nudge.Delivery
	for _, d := range c.got {
		if d.DeviceID == dev {
			out = append(out, d)
		}
	}
	return out
}

type env struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	q    *persistence.Queries
	repo *persistence.NudgeRepo
	cell int64
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, sql)
	}
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := persistence.New(pool)
	cell, err := geo.CellIDFromLatLng(testLat, testLng)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, ctx: ctx, pool: pool, q: q, repo: persistence.NewNudgeRepo(q, pool), cell: cell}
}

// device inserts a push-active device with an opted-in nudge subscription at
// the test cell and removes everything it owns on cleanup.
func (e *env) device(tz string, sendHour int) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.exec(`INSERT INTO t_devices (id, tz, locale, apns_token, apns_env, push_status)
		VALUES ($1::uuid, $2, 'en', 'tok-'||$1::text, 'sandbox', 'active')`, id, tz)
	e.exec(`INSERT INTO t_nudge_subscriptions (device_id, home_cell, home_source, home_updated_at, send_hour, consent)
		VALUES ($1, $2, 'location', now(), $3, 'explicit')`, id, e.cell, sendHour)
	e.t.Cleanup(func() { _, _ = e.pool.Exec(e.ctx, `DELETE FROM t_devices WHERE id=$1`, id) })
	return id
}

// spot inserts an active spot at the test point with hourly scores for
// `days` local days (Asia/Ho_Chi_Minh) from `first`: hours 10..18 score
// scores[day]. Removed on cleanup (scores cascade).
func (e *env) spot(first time.Time, scores ...int) uuid.UUID {
	e.t.Helper()
	var cfg int
	if err := e.pool.QueryRow(e.ctx, `SELECT version FROM t_scoring_configs LIMIT 1`).Scan(&cfg); err != nil {
		e.t.Skipf("no scoring config in the test database: %v", err)
	}
	// The spot lives in a pack of its own (draft, nudgetest- slug) so it never
	// depends on, or is deleted with, another package's test pack. Cleanups run
	// last-in first-out: the spot goes before its pack.
	pack := uuid.New()
	e.exec(`INSERT INTO t_city_packs (id, slug, name, country, tz, boundary, status)
		VALUES ($1, $2, 'Nudge test', 'VN', 'Asia/Ho_Chi_Minh', ST_Multi(ST_MakeEnvelope($3,$4,$5,$6,4326)), 'draft')`,
		pack, "nudgetest-"+pack.String(), testLng-0.01, testLat-0.01, testLng+0.01, testLat+0.01)
	e.t.Cleanup(func() { _, _ = e.pool.Exec(e.ctx, `DELETE FROM t_city_packs WHERE id=$1`, pack) })
	id := uuid.New()
	e.exec(`INSERT INTO t_spots (id, city_pack_id, kind, name, geom, country, tz, cell_id, status, quality)
		VALUES ($1, $2, 'park', $3, ST_SetSRID(ST_MakePoint($4,$5),4326)::geography, 'VN', 'Asia/Ho_Chi_Minh', $6, 'active', 50)`,
		id, pack, "nudge-test-"+id.String()[:8], testLng, testLat, e.cell)
	e.t.Cleanup(func() { _, _ = e.pool.Exec(e.ctx, `DELETE FROM t_spots WHERE id=$1`, id) })
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	for day, sc := range scores {
		for h := 10; h < 19; h++ {
			ts := time.Date(first.Year(), first.Month(), first.Day()+day, h, 0, 0, 0, loc)
			e.exec(`INSERT INTO t_spot_scores (spot_id, ts_hour, config_version, score, reasons, confidence, computed_at)
				VALUES ($1, $2, $3, $4, '[]'::jsonb, 3, now())`, id, ts, cfg, sc)
		}
	}
	return id
}

func (e *env) svc(snd nudge.Sender) *nudge.Service {
	return nudge.NewService(e.repo, snd, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nudge.Config{MinScore: 80, RadiusM: 5000})
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// Monday 2026-12-07: 08:00 in Ho Chi Minh is 01:00Z, 08:00 in Amsterdam is
// 07:00Z (CET, no DST in December).
var (
	mondayHCM = time.Date(2026, 12, 7, 1, 0, 0, 0, time.UTC)
	mondayAMS = time.Date(2026, 12, 7, 7, 0, 0, 0, time.UTC)
	monday    = time.Date(2026, 12, 7, 0, 0, 0, 0, time.UTC)
)

func TestRunDBSendHourFollowsDeviceTimeZone(t *testing.T) {
	e := setup(t)
	spot := e.spot(monday, 92, 60, 60, 60, 60, 60, 60)
	hcm := e.device("Asia/Ho_Chi_Minh", 8)
	ams := e.device("Europe/Amsterdam", 8)
	snd := &capture{}
	svc := e.svc(snd)

	if _, err := svc.Run(e.ctx, mondayHCM); err != nil {
		t.Fatal(err)
	}
	if len(snd.to(hcm)) != 1 || len(snd.to(ams)) != 0 {
		t.Fatalf("at 08:00 HCM only the HCM device is due: hcm=%d ams=%d", len(snd.to(hcm)), len(snd.to(ams)))
	}
	d := snd.to(hcm)[0]
	if d.SpotID != spot || d.Score != 92 || d.Slot != nudge.SlotDay || !d.LocalDate.Equal(monday) {
		t.Fatalf("delivery: %+v", d)
	}

	// Same instant again: delivery is sent, ledger has a nudge row -> nothing.
	if _, err := svc.Run(e.ctx, mondayHCM.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(snd.to(hcm)) != 1 {
		t.Fatalf("resent within the same local day: %d", len(snd.to(hcm)))
	}

	// Rows written.
	var status string
	var sentAt *time.Time
	if err := e.pool.QueryRow(e.ctx, `SELECT status, sent_at FROM t_nudge_deliveries WHERE device_id=$1`, hcm).Scan(&status, &sentAt); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || sentAt == nil {
		t.Fatalf("delivery row: %s %v", status, sentAt)
	}
	if n := e.count(`SELECT count(*) FROM t_push_sends WHERE device_id=$1 AND kind='nudge.goodday'`, hcm); n != 1 {
		t.Fatalf("push ledger rows = %d", n)
	}
	var streak int
	var lastSent *time.Time
	if err := e.pool.QueryRow(e.ctx, `SELECT ignored_streak, last_sent_at FROM t_nudge_subscriptions WHERE device_id=$1`, hcm).Scan(&streak, &lastSent); err != nil {
		t.Fatal(err)
	}
	if streak != 0 || lastSent == nil {
		t.Fatalf("subscription after first send: streak=%d last=%v", streak, lastSent)
	}

	// 08:00 in Amsterdam (14:00 HCM): now the Amsterdam device is due. The
	// day's hours 10-14 local to the spot have started, 15-18 remain.
	if _, err := svc.Run(e.ctx, mondayAMS); err != nil {
		t.Fatal(err)
	}
	if len(snd.to(ams)) != 1 || len(snd.to(hcm)) != 1 {
		t.Fatalf("at 08:00 AMS: ams=%d hcm=%d", len(snd.to(ams)), len(snd.to(hcm)))
	}
}

func TestClaimIdempotencyAndRetryDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	spot := e.spot(monday, 90)
	del := nudge.Delivery{
		DeviceID: dev, Slot: nudge.SlotDay, LocalDate: monday, TargetDate: monday, SpotID: spot, SpotName: "x",
		WindowStart: mondayHCM.Add(2 * time.Hour), WindowEnd: mondayHCM.Add(5 * time.Hour), Score: 90,
	}

	if ok, err := e.repo.Claim(e.ctx, del, mondayHCM); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, err := e.repo.Claim(e.ctx, del, mondayHCM); err != nil || ok {
		t.Fatalf("second claim must lose: %v %v", ok, err)
	}
	if ok, _ := e.repo.Reclaim(e.ctx, del, mondayHCM); ok {
		t.Fatal("a claimed row is not reclaimable")
	}
	if err := e.repo.MarkFailed(e.ctx, del, "apns 500"); err != nil {
		t.Fatal(err)
	}

	// Candidates reports the failed delivery so the service can retry it.
	cands, err := e.repo.Candidates(e.ctx, mondayHCM.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var c *nudge.Candidate
	for i := range cands {
		if cands[i].DeviceID == dev {
			c = &cands[i]
		}
	}
	if c == nil || c.LastDeliveryStatus != "failed" || c.LastDeliveryAttempts != 1 || c.LastDeliveryDate == nil || !c.LastDeliveryDate.Equal(monday) {
		t.Fatalf("candidate: %+v", c)
	}

	// Attempts 1 -> 2 -> 3, then the third failure is final.
	for want := 2; want <= nudge.MaxDeliveryAttempts; want++ {
		if ok, err := e.repo.Reclaim(e.ctx, del, mondayHCM); err != nil || !ok {
			t.Fatalf("reclaim to attempt %d: %v %v", want, ok, err)
		}
		if err := e.repo.MarkFailed(e.ctx, del, "apns 500"); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := e.repo.Reclaim(e.ctx, del, mondayHCM); ok {
		t.Fatal("attempts exhausted: no more reclaim")
	}
	if err := e.repo.Sweep(e.ctx, mondayHCM); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = e.pool.QueryRow(e.ctx, `SELECT status FROM t_nudge_deliveries WHERE device_id=$1`, dev).Scan(&status)
	if status != "dead" {
		t.Fatalf("exhausted delivery is dead-lettered by the sweep, got %q", status)
	}
}

func TestSweepStaleClaimDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	spot := e.spot(monday, 90)
	del := nudge.Delivery{DeviceID: dev, Slot: nudge.SlotDay, LocalDate: monday, TargetDate: monday, SpotID: spot,
		WindowStart: mondayHCM, WindowEnd: mondayHCM.Add(3 * time.Hour), Score: 90}
	if ok, _ := e.repo.Claim(e.ctx, del, mondayHCM); !ok {
		t.Fatal("claim")
	}
	if err := e.repo.Sweep(e.ctx, mondayHCM.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM t_nudge_deliveries WHERE device_id=$1 AND status='claimed'`, dev); n != 1 {
		t.Fatal("a fresh claim must survive the sweep")
	}
	if err := e.repo.Sweep(e.ctx, mondayHCM.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM t_nudge_deliveries WHERE device_id=$1 AND status='failed'`, dev); n != 1 {
		t.Fatal("a claim older than 5 minutes becomes failed")
	}
}

func candFor(t *testing.T, e *env, dev uuid.UUID, now time.Time) *nudge.Candidate {
	t.Helper()
	cands, err := e.repo.Candidates(e.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cands {
		if cands[i].DeviceID == dev {
			return &cands[i]
		}
	}
	return nil
}

func TestCandidateEligibilityDB(t *testing.T) {
	e := setup(t)
	now := mondayHCM

	t.Run("a push of any kind within 20h excludes, 21h does not", func(t *testing.T) {
		dev := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`INSERT INTO t_push_sends (device_id, kind, sent_at) VALUES ($1,'alert.window',$2)`, dev, now.Add(-19*time.Hour))
		if candFor(t, e, dev, now) != nil {
			t.Fatal("alert 19h ago must exclude")
		}
		e.exec(`DELETE FROM t_push_sends WHERE device_id=$1`, dev)
		e.exec(`INSERT INTO t_push_sends (device_id, kind, sent_at) VALUES ($1,'digest.weekend',$2)`, dev, now.Add(-21*time.Hour))
		c := candFor(t, e, dev, now)
		if c == nil {
			t.Fatal("digest 21h ago must not exclude")
		}
		if c.DigestSentAt == nil || !c.DigestSentAt.Equal(now.Add(-21*time.Hour)) {
			t.Fatalf("digest sent_at not surfaced: %+v", c.DigestSentAt)
		}
	})

	t.Run("two nudges in 7 days is the cap", func(t *testing.T) {
		dev := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`INSERT INTO t_push_sends (device_id, kind, sent_at) VALUES ($1,'nudge.goodday',$2),($1,'nudge.goodday',$3)`,
			dev, now.Add(-30*time.Hour), now.Add(-6*24*time.Hour))
		if candFor(t, e, dev, now) != nil {
			t.Fatal("two nudges in the last 7 days must exclude")
		}
		e.exec(`UPDATE t_push_sends SET sent_at=$2 WHERE device_id=$1 AND sent_at=$3`, dev, now.Add(-8*24*time.Hour), now.Add(-6*24*time.Hour))
		if candFor(t, e, dev, now) == nil {
			t.Fatal("one nudge in 7 days is under the cap")
		}
	})

	t.Run("recently active device is not a candidate, quiet one is", func(t *testing.T) {
		dev := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`UPDATE t_devices SET last_seen_at=$2 WHERE id=$1`, dev, now.Add(-23*time.Hour))
		if candFor(t, e, dev, now) != nil {
			t.Fatal("seen 23h ago must exclude")
		}
		e.exec(`UPDATE t_devices SET last_seen_at=$2 WHERE id=$1`, dev, now.Add(-25*time.Hour))
		if candFor(t, e, dev, now) == nil {
			t.Fatal("seen 25h ago is quiet")
		}
	})

	t.Run("opt-out, no cell, no push, deleted", func(t *testing.T) {
		off := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`UPDATE t_nudge_subscriptions SET enabled=false WHERE device_id=$1`, off)
		nocell := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`UPDATE t_nudge_subscriptions SET home_cell=NULL, home_source=NULL, home_updated_at=NULL WHERE device_id=$1`, nocell)
		nopush := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`UPDATE t_devices SET push_status='unregistered', apns_token=NULL WHERE id=$1`, nopush)
		gone := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`UPDATE t_devices SET deleted_at=now() WHERE id=$1`, gone)
		for name, id := range map[string]uuid.UUID{"opted out": off, "no home cell": nocell, "push not active": nopush, "deleted": gone} {
			if candFor(t, e, id, now) != nil {
				t.Fatalf("%s must not be a candidate", name)
			}
		}
	})

	t.Run("ignored-nudge back-off", func(t *testing.T) {
		set := func(dev uuid.UUID, streak int, sentAgo time.Duration, seenAfter bool) {
			e.exec(`UPDATE t_nudge_subscriptions SET ignored_streak=$2, last_sent_at=$3 WHERE device_id=$1`, dev, streak, now.Add(-sentAgo))
			if seenAfter {
				e.exec(`UPDATE t_devices SET last_seen_at=$2 WHERE id=$1`, dev, now.Add(-sentAgo+time.Hour))
			} else {
				e.exec(`UPDATE t_devices SET last_seen_at=NULL WHERE id=$1`, dev)
			}
		}
		dev := e.device("Asia/Ho_Chi_Minh", 8)
		cases := []struct {
			name      string
			streak    int
			sentAgo   time.Duration
			seenAfter bool
			want      bool
		}{
			{"stored 0, last nudge ignored (effective 1)", 0, 3 * 24 * time.Hour, false, true},
			{"stored 1 -> effective 2, still fine", 1, 3 * 24 * time.Hour, false, true},
			{"stored 2 -> effective 3, within 14 days", 2, 10 * 24 * time.Hour, false, false},
			{"stored 2 -> effective 3, after 14 days", 2, 15 * 24 * time.Hour, false, true},
			{"stored 5 -> effective 6 is paused", 5, 60 * 24 * time.Hour, false, false},
			{"opened the app after the last nudge resets", 5, 10 * 24 * time.Hour, true, true},
		}
		for _, tc := range cases {
			set(dev, tc.streak, tc.sentAgo, tc.seenAfter)
			c := candFor(t, e, dev, now)
			if (c != nil) != tc.want {
				t.Fatalf("%s: candidate=%v want %v", tc.name, c != nil, tc.want)
			}
		}
	})

	t.Run("weekly digest flag is surfaced", func(t *testing.T) {
		dev := e.device("Asia/Ho_Chi_Minh", 8)
		e.exec(`INSERT INTO t_weekly_digests (device_id, spot_ids, enabled) VALUES ($1,'{}',true)`, dev)
		if c := candFor(t, e, dev, now); c == nil || !c.DigestEnabled {
			t.Fatalf("candidate: %+v", c)
		}
		e.exec(`UPDATE t_weekly_digests SET enabled=false WHERE device_id=$1`, dev)
		if c := candFor(t, e, dev, now); c == nil || c.DigestEnabled {
			t.Fatalf("candidate: %+v", c)
		}
	})
}

func TestDeviceDeleteRemovesNudgeDataDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	spot := e.spot(monday, 90)
	del := nudge.Delivery{DeviceID: dev, Slot: nudge.SlotDay, LocalDate: monday, TargetDate: monday, SpotID: spot,
		WindowStart: mondayHCM, WindowEnd: mondayHCM.Add(3 * time.Hour), Score: 90}
	if ok, _ := e.repo.Claim(e.ctx, del, mondayHCM); !ok {
		t.Fatal("claim")
	}
	if err := e.repo.RecordPushSend(e.ctx, dev, nudge.Kind, mondayHCM); err != nil {
		t.Fatal(err)
	}

	// DELETE /v1/devices/:id is a soft delete, so FK cascades never fire: the
	// location-derived data must be removed explicitly.
	if ok, err := persistence.NewDevicesRepo(e.q, e.pool).Delete(e.ctx, dev); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, tbl := range []string{"t_nudge_subscriptions", "t_nudge_deliveries", "t_push_sends"} {
		if n := e.count(`SELECT count(*) FROM `+tbl+` WHERE device_id=$1`, dev); n != 0 {
			t.Fatalf("%s survived device delete: %d rows", tbl, n)
		}
	}
}

func TestSubscriptionRoundTripDB(t *testing.T) {
	e := setup(t)
	id := uuid.New()
	e.exec(`INSERT INTO t_devices (id, tz, locale) VALUES ($1::uuid,'Asia/Ho_Chi_Minh','en')`, id)
	t.Cleanup(func() { _, _ = e.pool.Exec(e.ctx, `DELETE FROM t_devices WHERE id=$1`, id) })
	svc := e.svc(nil)

	consent := nudge.ConsentProvisional
	s, err := svc.Put(e.ctx, id, nudge.PutInput{Consent: &consent, Home: &nudge.Home{Lat: testLat, Lng: testLng, Source: nudge.SourceCity}})
	if err != nil {
		t.Fatal(err)
	}
	if s.HomeCell == nil || *s.HomeCell != e.cell || !s.Enabled || s.SendHour != 8 {
		t.Fatalf("created: %+v", s)
	}
	if s.HomeUpdatedAt == nil {
		t.Fatal("home updated-at missing")
	}
	updatedAt := *s.HomeUpdatedAt

	off := false
	s, err = svc.Put(e.ctx, id, nudge.PutInput{Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled || s.HomeCell == nil || *s.HomeCell != e.cell || s.HomeUpdatedAt == nil || !s.HomeUpdatedAt.Equal(updatedAt) || s.Consent != consent {
		t.Fatalf("an update without home keeps cell, updated-at and consent: %+v", s)
	}
	if _, err := svc.Get(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	// A delivery names a spot near home: opt-out must remove it too.
	spot := e.spot(monday, 90)
	del := nudge.Delivery{DeviceID: id, Slot: nudge.SlotDay, LocalDate: monday, TargetDate: monday, SpotID: spot,
		WindowStart: mondayHCM, WindowEnd: mondayHCM.Add(3 * time.Hour), Score: 90}
	if ok, err := e.repo.Claim(e.ctx, del, mondayHCM); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := svc.Delete(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM t_nudge_deliveries WHERE device_id=$1`, id); n != 0 {
		t.Fatalf("opt-out left %d delivery rows", n)
	}
	if _, err := svc.Get(e.ctx, id); err != nudge.ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	// Soft-deleted devices are unknown to the opt-in.
	e.exec(`UPDATE t_devices SET deleted_at=now() WHERE id=$1`, id)
	if _, err := svc.Put(e.ctx, id, nudge.PutInput{Consent: &consent, Home: &nudge.Home{Lat: testLat, Lng: testLng, Source: nudge.SourceCity}}); err != nudge.ErrDeviceInvalid {
		t.Fatalf("deleted device: %v", err)
	}
}

func TestNoHomeOptInDB(t *testing.T) {
	e := setup(t)
	id := uuid.New()
	e.exec(`INSERT INTO t_devices (id, tz, locale, apns_token, apns_env, push_status)
		VALUES ($1::uuid,'Asia/Ho_Chi_Minh','en','tok-'||$1::text,'sandbox','active')`, id)
	t.Cleanup(func() { _, _ = e.pool.Exec(e.ctx, `DELETE FROM t_devices WHERE id=$1`, id) })
	svc := e.svc(nil)
	consent := nudge.ConsentExplicit

	s, err := svc.Put(e.ctx, id, nudge.PutInput{Consent: &consent})
	if err != nil || s.HomeSet() || s.HomeUpdatedAt != nil {
		t.Fatalf("no-home opt-in: %+v %v", s, err)
	}
	if candFor(t, e, id, mondayHCM) != nil {
		t.Fatal("a subscription without a home is not eligible")
	}
	s, err = svc.Put(e.ctx, id, nudge.PutInput{Home: &nudge.Home{Lat: testLat, Lng: testLng, Source: nudge.SourceLocation}})
	if err != nil || !s.HomeSet() || s.HomeUpdatedAt == nil || s.HomeSource != nudge.SourceLocation {
		t.Fatalf("home follows: %+v %v", s, err)
	}
	if candFor(t, e, id, mondayHCM) == nil {
		t.Fatal("with a home the quiet device is a candidate")
	}
	// The DB keeps home_cell / home_source / home_updated_at all-or-nothing.
	if _, err := e.pool.Exec(e.ctx, `UPDATE t_nudge_subscriptions SET home_source=NULL WHERE device_id=$1`, id); err == nil {
		t.Fatal("half a home must be rejected by the CHECK")
	}
}

func TestScoresNearRankingAndRadiusDB(t *testing.T) {
	e := setup(t)
	spot := e.spot(monday, 90)
	from := mondayHCM.Add(-24 * time.Hour)
	to := from.Add(48 * time.Hour)

	rows, err := e.repo.ScoresNear(e.ctx, e.cell, 5000, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 || rows[0].SpotID != spot || rows[0].Score != 90 || rows[0].TZ != "Asia/Ho_Chi_Minh" || rows[0].Quality != 50 {
		t.Fatalf("rows: %d %+v", len(rows), rows)
	}
	// Outside the radius of the cell center: nothing.
	far, _ := e.repo.ScoresNear(e.ctx, mustCell(t, testLat+1, testLng), 5000, from, to)
	for _, r := range far {
		if r.SpotID == spot {
			t.Fatal("a spot ~110 km away must not be returned")
		}
	}
	// Inactive spots are never suggested.
	e.exec(`UPDATE t_spots SET status='hidden' WHERE id=$1`, spot)
	rows, _ = e.repo.ScoresNear(e.ctx, e.cell, 5000, from, to)
	for _, r := range rows {
		if r.SpotID == spot {
			t.Fatal("hidden spot returned")
		}
	}
}

func mustCell(t *testing.T, lat, lng float64) int64 {
	t.Helper()
	c, err := geo.CellIDFromLatLng(lat, lng)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPurgePushSendsDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	// Far in the past so the cutoff below cannot touch anyone else's rows.
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	e.exec(`INSERT INTO t_push_sends (device_id, kind, sent_at) VALUES ($1,'nudge.goodday',$2),($1,'nudge.goodday',now())`, dev, old)
	n, err := e.q.PurgePushSends(e.ctx, persistence.PurgePushSendsParams{SentAt: old.Add(time.Hour), Limit: 1000})
	if err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	if got := e.count(`SELECT count(*) FROM t_push_sends WHERE device_id=$1`, dev); got != 1 {
		t.Fatalf("recent row must survive, have %d", got)
	}
}

// Decision 1 meets §4: a Friday day push blocks the Friday 18:00 weekend push
// (20 h cap, one push per day by design), while a Friday with no morning push
// gets the weekend slot.
func TestFridayWeekendSlotAndCapDB(t *testing.T) {
	e := setup(t)
	// Friday 2026-12-04; the weekend is Sat 5 / Sun 6.
	friday := time.Date(2026, 12, 4, 0, 0, 0, 0, time.UTC)
	e.spot(friday, 60, 93, 60) // Fri, Sat, Sun: only Saturday is good
	morning := e.device("Asia/Ho_Chi_Minh", 8)
	quiet := e.device("Asia/Ho_Chi_Minh", 8)
	snd := &capture{}
	svc := e.svc(snd)

	// Friday 08:00 HCM = 01:00Z: Friday itself scores 60, so nobody gets a day push.
	hcm8 := time.Date(2026, 12, 4, 1, 0, 0, 0, time.UTC)
	if _, err := svc.Run(e.ctx, hcm8); err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 0 {
		t.Fatalf("no good Friday: %d sends", len(snd.got))
	}
	// Give one device an earlier push the same Friday morning (10 h before 18:00).
	e.exec(`INSERT INTO t_push_sends (device_id, kind, sent_at) VALUES ($1,'alert.window',$2)`, morning, hcm8.Add(10*time.Hour-9*time.Hour))

	// Friday 18:00 HCM = 11:00Z.
	if _, err := svc.Run(e.ctx, time.Date(2026, 12, 4, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if len(snd.to(morning)) != 0 {
		t.Fatal("a push 10 h earlier must block the weekend slot (20 h cap)")
	}
	got := snd.to(quiet)
	if len(got) != 1 || got[0].Slot != nudge.SlotWeekend || got[0].Score != 93 {
		t.Fatalf("weekend slot: %+v", got)
	}
}

// last_seen_at drives "has gone quiet": TouchDevice must set it, but write at
// most once per hour.
func TestTouchDeviceThrottleDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	repo := persistence.NewDevicesRepo(e.q, e.pool)
	seen := func() (ts time.Time, null bool) {
		var p *time.Time
		if err := e.pool.QueryRow(e.ctx, `SELECT last_seen_at FROM t_devices WHERE id=$1`, dev).Scan(&p); err != nil {
			t.Fatal(err)
		}
		if p == nil {
			return time.Time{}, true
		}
		return *p, false
	}

	if _, null := seen(); !null {
		t.Fatal("fresh test device has no last_seen_at")
	}
	if err := repo.Touch(e.ctx, dev); err != nil {
		t.Fatal(err)
	}
	first, null := seen()
	if null || time.Since(first) > time.Minute {
		t.Fatalf("touch must set last_seen_at to now: %v", first)
	}
	if err := repo.Touch(e.ctx, dev); err != nil {
		t.Fatal(err)
	}
	if again, _ := seen(); !again.Equal(first) {
		t.Fatal("a second touch within the hour must not write")
	}
	e.exec(`UPDATE t_devices SET last_seen_at = now() - interval '61 minutes' WHERE id=$1`, dev)
	old, _ := seen()
	if err := repo.Touch(e.ctx, dev); err != nil {
		t.Fatal(err)
	}
	if after, _ := seen(); !after.After(old.Add(time.Hour)) {
		t.Fatal("after an hour the touch writes again")
	}
	// A touched device is "active": no longer a nudge candidate.
	if candFor(t, e, dev, time.Now()) != nil {
		t.Fatal("an active device must not get a nudge")
	}
}

func TestPurgeNudgeDeliveriesDB(t *testing.T) {
	e := setup(t)
	dev := e.device("Asia/Ho_Chi_Minh", 8)
	spot := e.spot(monday, 90)
	// Ancient dates keep the cutoff away from anyone else's rows.
	d1 := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2001, 1, 3, 0, 0, 0, 0, time.UTC)
	for _, d := range []time.Time{d1, d3} {
		e.exec(`INSERT INTO t_nudge_deliveries (device_id, local_date, slot, target_date, spot_id, window_start, window_end, score, status, attempts)
			VALUES ($1,$2,'day',$2,$3,now(),now(),90,'failed',1)`, dev, d, spot)
	}
	cut := time.Date(2001, 1, 2, 0, 0, 0, 0, time.UTC)
	n, err := e.q.PurgeNudgeDeliveries(e.ctx, persistence.PurgeNudgeDeliveriesParams{
		Before: pgtype.Date{Time: cut, Valid: true}, RowLimit: 1000})
	if err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	var left time.Time
	if err := e.pool.QueryRow(e.ctx, `SELECT local_date FROM t_nudge_deliveries WHERE device_id=$1`, dev).Scan(&left); err != nil || !left.Equal(d3) {
		t.Fatalf("the newer row must survive: %v %v", left, err)
	}
}
