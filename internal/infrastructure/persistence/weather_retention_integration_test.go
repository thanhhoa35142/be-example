package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/weather"
)

// A cell id no real H3 cell uses, so the test never touches real rows.
const testWeatherCell int64 = -4242

func weatherTS(t *testing.T, pool *pgxpool.Pool) map[time.Time]*float32 {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT ts, precip_mm FROM t_weather_hourly_forecasts WHERE cell_id = $1`, testWeatherCell)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[time.Time]*float32{}
	for rows.Next() {
		var ts time.Time
		var mm *float32
		if err := rows.Scan(&ts, &mm); err != nil {
			t.Fatal(err)
		}
		out[ts.UTC()] = mm
	}
	return out
}

func TestWeatherReplaceRowsKeepsPastHours(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := persistence.NewWeatherRepo(persistence.New(pool), pool)
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM t_weather_hourly_forecasts WHERE cell_id = $1`, testWeatherCell)
	}
	clean()
	t.Cleanup(clean)

	now := time.Now().UTC().Truncate(time.Hour)
	to := now.Add(weather.ForecastDays * 24 * time.Hour)
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	mm := func(v float64) *float64 { return &v }
	row := func(h int, p *float64) weather.Row {
		temp := 10.0
		return weather.Row{CellID: testWeatherCell, TS: at(h), TempC: &temp, PrecipMM: p,
			Provider: "open-meteo", FetchedAt: time.Now().UTC()}
	}

	// History: now-30h and now-10h, plus an old forecast for now+1h.
	if err := repo.ReplaceRows(ctx, testWeatherCell, at(-30), to,
		[]weather.Row{row(-30, mm(1.5)), row(-10, mm(0.5)), row(1, mm(9))}); err != nil {
		t.Fatal(err)
	}

	// 1. Forecast-only refresh (window from now): past rows survive, the
	// forecast is replaced, NULL precip stays NULL.
	if err := repo.ReplaceRows(ctx, testWeatherCell, now, to, []weather.Row{row(0, nil), row(1, mm(0))}); err != nil {
		t.Fatal(err)
	}
	got := weatherTS(t, pool)
	if len(got) != 4 || got[at(-30)] == nil || *got[at(-30)] != 1.5 || got[at(-10)] == nil {
		t.Fatalf("past rows not retained: %v", got)
	}
	if got[at(0)] != nil || got[at(1)] == nil || *got[at(1)] != 0 {
		t.Fatalf("forecast not replaced: now=%v +1h=%v", got[at(0)], got[at(1)])
	}

	// 2. Refresh with past hours from now-10h: [now-10h, to] replaced,
	// now-30h untouched.
	if err := repo.ReplaceRows(ctx, testWeatherCell, at(-10), to,
		[]weather.Row{row(-10, mm(2.25)), row(0, mm(0))}); err != nil {
		t.Fatal(err)
	}
	got = weatherTS(t, pool)
	if got[at(-30)] == nil || *got[at(-30)] != 1.5 {
		t.Fatalf("row older than the window was touched: %v", got)
	}
	if got[at(-10)] == nil || *got[at(-10)] != 2.25 {
		t.Fatalf("past row in window not replaced: %v", got[at(-10)])
	}
	if _, ok := got[at(1)]; ok || len(got) != 3 {
		t.Fatalf("stale forecast row not removed: %v", got)
	}

	// 3. Zero rows: nothing deleted.
	if err := repo.ReplaceRows(ctx, testWeatherCell, at(-48), to, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(weatherTS(t, pool)); n != 3 {
		t.Fatalf("empty refresh changed the cell: %d rows", n)
	}

	// 4. A failing insert (duplicate ts) rolls the delete back.
	if err := repo.ReplaceRows(ctx, testWeatherCell, at(-48), to,
		[]weather.Row{row(0, nil), row(0, nil)}); err == nil {
		t.Fatal("duplicate rows should fail")
	}
	if n := len(weatherTS(t, pool)); n != 3 {
		t.Fatalf("failed replace was not rolled back: %d rows", n)
	}

	// 5. The schema rejects negative amounts.
	_, err := pool.Exec(ctx, `INSERT INTO t_weather_hourly_forecasts (cell_id, ts, provider, fetched_at, precip_mm)
		VALUES ($1, $2, 'test', now(), -1)`, testWeatherCell, at(5))
	expectCheckViolation(t, err)
}
