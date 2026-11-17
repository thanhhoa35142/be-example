package getaways_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/getaways"
	"github.com/thanhhoa35142/be-example/internal/platform/testdb"
)

// Open-ocean square: no real spot is near it, and the boundary only has to be
// a valid MultiPolygon (the center is read from it).
const (
	oceanLat = -30.0
	oceanLng = -120.0
)

type dbEnv struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	q    *persistence.Queries
	repo *persistence.GetawaysRepo
	cfg  int16
	cell int64
	tag  string
}

func setupDB(t *testing.T) *dbEnv {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := persistence.New(pool)
	cell, err := geo.CellIDFromLatLng(oceanLat, oceanLng)
	if err != nil {
		t.Fatal(err)
	}
	e := &dbEnv{t: t, ctx: ctx, pool: pool, q: q, repo: persistence.NewGetawaysRepo(q, pool), cell: cell, tag: uuid.NewString()[:8]}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name='t_city_hour_outlook'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("t_city_hour_outlook missing: apply migration 000027 to the test database (err %v)", err)
	}
	caps, err := e.repo.ActiveCaps(ctx)
	if err != nil {
		t.Fatalf("active scoring config: %v", err)
	}
	e.cfg = caps.Version
	return e
}

func (e *dbEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, sql)
	}
}

type testPack struct {
	getaways.Pack
	spots []uuid.UUID // active spots, ordered by name
}

// pack inserts a live pack with n active spots (named <tag>-<slug>-001...)
// and removes everything on cleanup.
func (e *dbEnv) pack(slug, tz string, n int) testPack {
	e.t.Helper()
	id := uuid.New()
	full := fmt.Sprintf("gwtest-%s-%s", slug, e.tag)
	e.exec(`INSERT INTO t_city_packs (id, slug, name, country, tz, boundary, status, last_import_at)
		VALUES ($1, $2, $3, 'PT', $4, ST_Multi(ST_MakeEnvelope($5,$6,$7,$8,4326)), 'live', now())`,
		id, full, "Test "+slug, tz, oceanLng, oceanLat, oceanLng+0.2, oceanLat+0.2)
	e.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.pool.Exec(ctx, `DELETE FROM t_city_hour_outlook WHERE pack_id=$1`, id)
		_, _ = e.pool.Exec(ctx, `DELETE FROM t_spots WHERE city_pack_id=$1`, id)
		_, _ = e.pool.Exec(ctx, `DELETE FROM t_city_packs WHERE id=$1`, id)
	})
	tp := testPack{Pack: getaways.Pack{ID: id, Slug: full, Name: "Test " + slug, Country: "PT", TZ: tz, ActiveSpots: n}}
	for i := 1; i <= n; i++ {
		tp.spots = append(tp.spots, e.spot(id, tz, fmt.Sprintf("%s-%s-%03d", e.tag, slug, i), "active"))
	}
	return tp
}

func (e *dbEnv) spot(pack uuid.UUID, tz, name, status string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.exec(`INSERT INTO t_spots (id, city_pack_id, kind, name, geom, country, tz, cell_id, status, quality)
		VALUES ($1, $2, 'park', $3, ST_SetSRID(ST_MakePoint($4,$5),4326)::geography, 'PT', $6, $7, $8, 50)`,
		id, pack, name, oceanLng, oceanLat, tz, e.cell, status)
	return id
}

// scores fills every spot of the pack (any status) with one score row per hour
// in [from, to] (score 60, s_weather 70, no cap).
func (e *dbEnv) scores(pack uuid.UUID, from, to time.Time) {
	e.t.Helper()
	e.exec(`INSERT INTO t_spot_scores (spot_id, ts_hour, config_version, score, s_weather, cap_reason, reasons, confidence, computed_at)
		SELECT s.id, h, $2, 60, 70, NULL, '[]'::jsonb, 3, now()
		FROM t_spots s CROSS JOIN generate_series($3::timestamptz, $4::timestamptz, interval '1 hour') h
		WHERE s.city_pack_id = $1 AND s.status = 'active'`, pack, e.cfg, from, to)
}

func (e *dbEnv) set(spot uuid.UUID, ts time.Time, col string, val any) {
	e.t.Helper()
	e.exec(fmt.Sprintf(`UPDATE t_spot_scores SET %s = $3 WHERE spot_id=$1 AND ts_hour=$2`, col), spot, ts, val)
}

func utc(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestComputeHoursDB(t *testing.T) {
	e := setupDB(t)
	caps, err := e.repo.ActiveCaps(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Rain >= 90 {
		t.Fatalf("rain cap %d is too high for this test (needs < 90)", caps.Rain)
	}

	lis := e.pack("lis", "Europe/Lisbon", 30)
	syd := e.pack("syd", "Australia/Sydney", 30)
	from, to := utc(2026, 12, 7, 0), utc(2026, 12, 10, 0)
	e.scores(lis.ID, from, to)
	e.scores(syd.ID, from, to)
	// A closed spot never counts: even with the worst possible weather.
	closed := e.spot(lis.ID, "Europe/Lisbon", e.tag+"-lis-closed", "closed")
	e.exec(`INSERT INTO t_spot_scores (spot_id, ts_hour, config_version, score, s_weather, reasons, confidence, computed_at)
		SELECT $1, h, $2, 0, 0, '[]'::jsonb, 3, now() FROM generate_series($3::timestamptz, $4::timestamptz, interval '1 hour') h`,
		closed, e.cfg, from, to)

	d8 := func(h int) time.Time { return utc(2026, 12, 8, h) }
	// 10:00: rain cap on every spot, s_weather 90 -> capped to the config value.
	for _, s := range lis.spots {
		e.set(s, d8(10), "cap_reason", "rain")
		e.set(s, d8(10), "s_weather", 90)
	}
	// 11:00: 16 spots at 40, 14 at 90 -> median 40.
	for i, s := range lis.spots {
		v := 90
		if i < 16 {
			v = 40
		}
		e.set(s, d8(11), "s_weather", v)
	}
	// 12:00: spot #2 has the best total score.
	e.set(lis.spots[1], d8(12), "score", 95)
	// 13:00: 7 of 30 spots rainy -> rain share 7/30; s_weather untouched.
	for _, s := range lis.spots[:7] {
		e.set(s, d8(13), "cap_reason", "rain")
		e.set(s, d8(13), "s_weather", 70)
	}

	rows, err := e.repo.ComputeHours(e.ctx, lis.ID, caps, d8(0), d8(0).Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 12 || !rows[0].TSHour.Equal(d8(8)) || !rows[11].TSHour.Equal(d8(19)) {
		t.Fatalf("want hours 08-19 only, got %d rows: first %v", len(rows), rows[0].TSHour)
	}
	by := map[int]getaways.CityHour{}
	for _, r := range rows {
		by[r.TSHour.Hour()] = r
		if r.SpotsN != 30 {
			t.Fatalf("hour %d spots_n = %d, want 30 (the closed spot must not count)", r.TSHour.Hour(), r.SpotsN)
		}
		if !r.LocalDate.Equal(day(2026, 12, 8)) {
			t.Fatalf("hour %d local date = %v", r.TSHour.Hour(), r.LocalDate)
		}
	}
	if by[9].WeatherScore != 70 || by[9].RainShare != 0 {
		t.Fatalf("plain hour = %+v", by[9])
	}
	if want := min(90, caps.Rain); by[10].WeatherScore != want || by[10].RainShare != 1 {
		t.Fatalf("rain-capped hour = %+v, want score %d share 1", by[10], want)
	}
	if by[11].WeatherScore != 40 {
		t.Fatalf("median hour = %d, want 40", by[11].WeatherScore)
	}
	if by[12].TopSpotID != lis.spots[1] || by[12].TopSpotScore != 95 {
		t.Fatalf("top spot = %v/%d, want %v/95", by[12].TopSpotID, by[12].TopSpotScore, lis.spots[1])
	}
	// A spot without a name never becomes the hour's top spot, whatever its score.
	unnamed := e.spot(lis.ID, "Europe/Lisbon", "  ", "active") // blank, not NULL
	e.exec(`INSERT INTO t_spot_scores (spot_id, ts_hour, config_version, score, s_weather, reasons, confidence, computed_at)
		VALUES ($1, $2, $3, 99, 70, '[]'::jsonb, 3, now())`, unnamed, d8(14), e.cfg)
	rows14, err := e.repo.ComputeHours(e.ctx, lis.ID, caps, d8(14), d8(15))
	if err != nil || len(rows14) != 1 {
		t.Fatalf("hour 14: %v %v", rows14, err)
	}
	if rows14[0].TopSpotID == unnamed || rows14[0].TopSpotID == (uuid.UUID{}) || rows14[0].TopSpotScore != 60 {
		t.Fatalf("hour 14 top spot = %v/%d: the unnamed 99 must lose to a named spot", rows14[0].TopSpotID, rows14[0].TopSpotScore)
	}
	e.exec(`DELETE FROM t_spot_scores WHERE spot_id = $1`, unnamed)
	e.exec(`UPDATE t_spots SET status = 'closed' WHERE id = $1`, unnamed)

	// All scores tie at 60 elsewhere: the name-first spot wins.
	if by[9].TopSpotID != lis.spots[0] || by[9].TopSpotScore != 60 {
		t.Fatalf("tie-break top spot = %v/%d, want %v/60", by[9].TopSpotID, by[9].TopSpotScore, lis.spots[0])
	}
	if got := float64(by[13].RainShare); got < 0.2333 || got > 0.2334 {
		t.Fatalf("rain share = %v, want 7/30", got)
	}

	// Sydney (UTC+11 in December): local Dec 8 08:00-19:00 is Dec 7 21:00Z to
	// Dec 8 08:00Z and belongs to local date Dec 8, not Dec 7.
	srows, err := e.repo.ComputeHours(e.ctx, syd.ID, caps, utc(2026, 12, 7, 12), utc(2026, 12, 8, 12))
	if err != nil {
		t.Fatal(err)
	}
	if len(srows) != 12 || !srows[0].TSHour.Equal(utc(2026, 12, 7, 21)) || !srows[11].TSHour.Equal(utc(2026, 12, 8, 8)) {
		t.Fatalf("sydney rows = %d, first %v", len(srows), srows[0].TSHour)
	}
	for _, r := range srows {
		if !r.LocalDate.Equal(day(2026, 12, 8)) {
			t.Fatalf("sydney %v local date = %v, want 2026-12-08", r.TSHour, r.LocalDate)
		}
	}
}

// scoped limits the real repo to the test's own packs so the run never
// touches (or is touched by) the rest of the database.
type scoped struct {
	*persistence.GetawaysRepo
	mine   map[uuid.UUID]bool
	others []uuid.UUID
}

func (s scoped) Packs(ctx context.Context) ([]getaways.Pack, error) {
	all, err := s.GetawaysRepo.Packs(ctx)
	if err != nil {
		return nil, err
	}
	var out []getaways.Pack
	for _, p := range all {
		if s.mine[p.ID] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s scoped) DeleteExceptPacks(ctx context.Context, keep []uuid.UUID) (int64, error) {
	return s.GetawaysRepo.DeleteExceptPacks(ctx, append(append([]uuid.UUID{}, keep...), s.others...))
}

func (e *dbEnv) outlookCount(pack uuid.UUID, where string, args ...any) int {
	e.t.Helper()
	var n int
	q := `SELECT count(*) FROM t_city_hour_outlook WHERE pack_id = $1 ` + where
	if err := e.pool.QueryRow(e.ctx, q, append([]any{pack}, args...)...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A city-hour whose spots are all unnamed keeps its row with no top spot:
// the hour still counts for coverage and the nil id is stored as NULL.
func TestUnnamedOnlyHourStoresNullTopSpotDB(t *testing.T) {
	e := setupDB(t)
	caps, err := e.repo.ActiveCaps(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := e.pack("nn", "Europe/Lisbon", 30)
	from, to := utc(2026, 12, 8, 0), utc(2026, 12, 9, 0)
	e.scores(p.ID, from, to)
	e.exec(`UPDATE t_spots SET name = NULL WHERE city_pack_id = $1`, p.ID)

	rows, err := e.repo.ComputeHours(e.ctx, p.ID, caps, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 12 {
		t.Fatalf("got %d rows, want 12: hours without a named spot must stay", len(rows))
	}
	for _, r := range rows {
		if r.TopSpotID != (uuid.UUID{}) || r.SpotsN != 30 {
			t.Fatalf("hour %v: top %v spots %d", r.TSHour, r.TopSpotID, r.SpotsN)
		}
	}
	if err := e.repo.ReplaceHours(e.ctx, p.ID, from, rows, caps.Version, utc(2026, 12, 8, 6)); err != nil {
		t.Fatalf("storing a nil top spot must not hit the foreign key: %v", err)
	}
	out, err := e.repo.Outlook(e.ctx, day(2026, 12, 8), day(2026, 12, 8), from)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range out {
		if r.PackID == p.ID {
			n++
			if r.TopSpotID != (uuid.UUID{}) {
				t.Fatalf("stored top spot = %v, want none", r.TopSpotID)
			}
		}
	}
	if n != 12 {
		t.Fatalf("read back %d rows, want 12", n)
	}
}

func TestRefreshAndListDB(t *testing.T) {
	e := setupDB(t)
	lis := e.pack("lis", "Europe/Lisbon", 30)
	syd := e.pack("syd", "Australia/Sydney", 30)
	small := e.pack("small", "Europe/Lisbon", 29)

	now := time.Date(2026, 12, 7, 8, 20, 0, 0, time.UTC) // Monday, hourNow 08:00Z
	from, to := utc(2026, 12, 7, 6), utc(2026, 12, 10, 23)
	for _, p := range []testPack{lis, syd, small} {
		e.scores(p.ID, from, to)
	}
	// Lisbon Dec 8 is spoiled around 10:00-11:00 so Dec 9 is the better, dry day.
	for _, s := range lis.spots {
		e.set(s, utc(2026, 12, 8, 10), "cap_reason", "rain")
		e.set(s, utc(2026, 12, 8, 11), "s_weather", 40)
	}

	var others []uuid.UUID
	rows, err := e.pool.Query(e.ctx, `SELECT id FROM t_city_packs WHERE id NOT IN ($1, $2, $3)`, lis.ID, syd.ID, small.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		others = append(others, id)
	}
	rows.Close()
	repo := scoped{GetawaysRepo: e.repo, mine: map[uuid.UUID]bool{lis.ID: true, syd.ID: true, small.ID: true}, others: others}
	svc := getaways.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), getaways.Config{GoodScore: 65, HintScore: 80})

	// Stale rows: one older than a day (purged), one inside the window of
	// retention but before the refresh range (kept), and one for the pack that
	// is under the spot minimum (dropped).
	stale := func(pack uuid.UUID, ts time.Time) {
		e.exec(`INSERT INTO t_city_hour_outlook (pack_id, ts_hour, local_date, weather_score, rain_share, spots_n, config_version, computed_at)
			VALUES ($1, $2, $3::date, 50, 0, 30, $4, now())`, pack, ts, ts.Format("2006-01-02"), e.cfg)
	}
	stale(lis.ID, utc(2026, 12, 6, 6))   // hourNow-26h: purged
	stale(lis.ID, utc(2026, 12, 6, 9))   // hourNow-23h: kept
	stale(small.ID, utc(2026, 12, 8, 9)) // ineligible pack: removed

	st, err := svc.Refresh(e.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if st.Packs != 3 || st.Eligible != 2 || st.Ineligible != 1 || st.Refreshed != 2 || st.Failed != 0 || st.DaysDropped != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if e.outlookCount(lis.ID, `AND ts_hour = $2`, utc(2026, 12, 6, 6)) != 0 {
		t.Fatal("row older than 24h must be purged")
	}
	if e.outlookCount(lis.ID, `AND ts_hour = $2`, utc(2026, 12, 6, 9)) != 1 {
		t.Fatal("row younger than 24h and before the refresh range must stay")
	}
	if e.outlookCount(small.ID, ``) != 0 {
		t.Fatal("pack under the spot minimum must have no outlook rows")
	}
	if n := e.outlookCount(lis.ID, `AND local_date = '2026-12-08'`); n != 12 {
		t.Fatalf("lisbon Dec 8 rows = %d", n)
	}
	if n := e.outlookCount(syd.ID, `AND local_date = '2026-12-08'`); n != 12 {
		t.Fatalf("sydney Dec 8 rows = %d", n)
	}
	total := e.outlookCount(lis.ID, `AND ts_hour >= $2`, utc(2026, 12, 7, 7)) + e.outlookCount(syd.ID, `AND ts_hour >= $2`, utc(2026, 12, 7, 7))
	if total != st.Written {
		t.Fatalf("stored %d rows, stats say %d", total, st.Written)
	}

	// Idempotent: a second run leaves the same rows.
	st2, err := svc.Refresh(e.ctx, now)
	if err != nil || st2.Written != st.Written {
		t.Fatalf("second run %+v err %v", st2, err)
	}
	if n := e.outlookCount(lis.ID, `AND ts_hour >= $2`, utc(2026, 12, 7, 7)) + e.outlookCount(syd.ID, `AND ts_hour >= $2`, utc(2026, 12, 7, 7)); n != total {
		t.Fatalf("rows after re-run = %d, want %d", n, total)
	}

	// List: Dec 8-9, every city in the test.
	list := func() map[string]getaways.Item {
		res, err := svc.List(e.ctx, getaways.Query{Start: day(2026, 12, 8), Days: 2, Limit: 39, Plan: "plus"}, now)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]getaways.Item{}
		for _, it := range res.Items {
			out[it.Slug] = it
		}
		if res.ComputedAt == nil {
			t.Fatal("computedAt must be set")
		}
		return out
	}
	items := list()
	if _, ok := items[small.Slug]; ok {
		t.Fatal("ineligible pack listed")
	}
	l, ok := items[lis.Slug]
	if !ok {
		t.Fatalf("lisbon missing from %v", items)
	}
	if l.WindowDays != 2 || !l.BestDay.Equal(day(2026, 12, 9)) || l.Window.WeatherScore != 70 ||
		l.Window.End.Sub(l.Window.Start) != 4*time.Hour {
		t.Fatalf("lisbon = %+v", l)
	}
	if len(l.Reasons) != 2 || l.Reasons[0].Code != getaways.ReasonDryDay || l.Reasons[1].Code != getaways.ReasonLongWindow {
		t.Fatalf("lisbon reasons = %+v (Dec 9 is dry with a 4h window)", l.Reasons)
	}
	if l.TopSpot == nil || l.TopSpot.ID != lis.spots[0] || l.TopSpot.Name == nil || *l.TopSpot.Name != fmt.Sprintf("%s-lis-001", e.tag) {
		t.Fatalf("lisbon top spot = %+v", l.TopSpot)
	}
	if l.Lon < oceanLng || l.Lon > oceanLng+0.2 || l.Lat < oceanLat || l.Lat > oceanLat+0.2 {
		t.Fatalf("lisbon center = %v,%v is outside its boundary", l.Lon, l.Lat)
	}
	s, ok := items[syd.Slug]
	if !ok {
		t.Fatal("sydney missing")
	}
	if !s.BestDay.Equal(day(2026, 12, 8)) || !s.Window.Start.Equal(utc(2026, 12, 7, 21)) || s.WindowDays != 2 {
		t.Fatalf("sydney = %+v (local Dec 8 starts at 21:00Z the day before)", s)
	}

	// Coverage: thin out Lisbon Dec 9 14:00Z to 10 of 30 spots. The day must
	// disappear, including the rows stored by the first run.
	for _, sp := range lis.spots[:20] {
		e.exec(`DELETE FROM t_spot_scores WHERE spot_id=$1 AND ts_hour=$2`, sp, utc(2026, 12, 9, 14))
	}
	st3, err := svc.Refresh(e.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if st3.DaysDropped != 1 {
		t.Fatalf("stats after thinning = %+v", st3)
	}
	if n := e.outlookCount(lis.ID, `AND local_date = '2026-12-09'`); n != 0 {
		t.Fatalf("lisbon Dec 9 still has %d rows", n)
	}
	if n := e.outlookCount(lis.ID, `AND local_date = '2026-12-08'`); n != 12 {
		t.Fatalf("lisbon Dec 8 rows = %d, want untouched", n)
	}
	items = list()
	if l := items[lis.Slug]; l.WindowDays != 1 || !l.BestDay.Equal(day(2026, 12, 8)) {
		t.Fatalf("lisbon after drop = %+v", l)
	}
	if s := items[syd.Slug]; s.WindowDays != 2 {
		t.Fatalf("sydney must be unaffected: %+v", s)
	}
}

func TestListSpotReasonDB(t *testing.T) {
	e := setupDB(t)
	lis := e.pack("lis", "Europe/Lisbon", 30)
	now := time.Date(2026, 12, 7, 8, 20, 0, 0, time.UTC)
	e.scores(lis.ID, utc(2026, 12, 7, 6), utc(2026, 12, 10, 23))

	// Dec 8: a 2h window only (rain on the day, other hours poor) -> no
	// dry_day/long_window, so the first weather.* reason of the top spot shows.
	for _, s := range lis.spots {
		for h := 8; h <= 19; h++ {
			ts := utc(2026, 12, 8, h)
			v := 30
			if h == 10 || h == 11 {
				v = 80
			}
			e.set(s, ts, "s_weather", v)
		}
		e.set(s, utc(2026, 12, 8, 15), "cap_reason", "rain")
	}
	top := lis.spots[0]
	e.exec(`UPDATE t_spot_scores SET reasons = $3::jsonb WHERE spot_id=$1 AND ts_hour=$2`, top, utc(2026, 12, 8, 10),
		`[{"code":"cap.dark","params":{}},{"code":"weather.clear","params":{"cloud":10}},{"code":"weather.hot","params":{}}]`)

	repo := scoped{GetawaysRepo: e.repo, mine: map[uuid.UUID]bool{lis.ID: true}}
	rs, err := e.pool.Query(e.ctx, `SELECT id FROM t_city_packs WHERE id <> $1`, lis.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var id uuid.UUID
		_ = rs.Scan(&id)
		repo.others = append(repo.others, id)
	}
	rs.Close()
	svc := getaways.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), getaways.Config{})
	if _, err := svc.Refresh(e.ctx, now); err != nil {
		t.Fatal(err)
	}
	res, err := svc.List(e.ctx, getaways.Query{Start: day(2026, 12, 8), Days: 1, Plan: "plus"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var it *getaways.Item
	for i := range res.Items {
		if res.Items[i].Slug == lis.Slug {
			it = &res.Items[i]
		}
	}
	if it == nil {
		t.Fatalf("lisbon missing: %+v", res.Items)
	}
	if it.Window.Start != utc(2026, 12, 8, 10) || it.Window.End != utc(2026, 12, 8, 12) {
		t.Fatalf("window = %+v", it.Window)
	}
	if len(it.Reasons) != 1 || it.Reasons[0].Code != "weather.clear" || it.Reasons[0].Params["cloud"] != float64(10) {
		t.Fatalf("reasons = %+v, want the first weather.* reason of the top spot at the window start", it.Reasons)
	}
}
