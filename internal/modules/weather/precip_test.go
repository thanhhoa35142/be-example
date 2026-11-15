package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

func serveHourly(t *testing.T, hourly map[string]any, gotQuery *url.Values) *OpenMeteo {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotQuery != nil {
			*gotQuery = r.URL.Query()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hourly": hourly})
	}))
	t.Cleanup(srv.Close)
	return NewOpenMeteo(srv.Client(), srv.URL+"/v1/forecast", "gfs_global")
}

func forecast(t *testing.T, p *OpenMeteo) []Point {
	t.Helper()
	pts, err := p.Forecast(context.Background(), 52, 4,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	return pts
}

func TestOpenMeteoPrecipitationParsing(t *testing.T) {
	times := []string{"2026-01-01T00:00", "2026-01-01T01:00", "2026-01-01T02:00", "2026-01-01T03:00"}

	t.Run("present, null and negative", func(t *testing.T) {
		var q url.Values
		p := serveHourly(t, map[string]any{
			"time":           times,
			"temperature_2m": []any{1.0, 2.0, 3.0, 4.0},
			"precipitation":  []any{0.0, 1.7, nil, -0.1},
		}, &q)
		pts := forecast(t, p)
		if len(pts) != 4 {
			t.Fatalf("points = %d", len(pts))
		}
		if pts[0].PrecipMM == nil || *pts[0].PrecipMM != 0 {
			t.Errorf("0 mm must be kept as 0, got %v", pts[0].PrecipMM)
		}
		if pts[1].PrecipMM == nil || *pts[1].PrecipMM != 1.7 {
			t.Errorf("PrecipMM[1] = %v, want 1.7", pts[1].PrecipMM)
		}
		if pts[2].PrecipMM != nil {
			t.Errorf("null must stay nil, got %v", *pts[2].PrecipMM)
		}
		if pts[3].PrecipMM != nil {
			t.Errorf("negative must be dropped, got %v", *pts[3].PrecipMM)
		}
		if !strings.Contains(q.Get("hourly"), "precipitation,") && !strings.HasSuffix(q.Get("hourly"), ",precipitation") {
			t.Errorf("hourly does not request precipitation: %q", q.Get("hourly"))
		}
		if q.Get("precipitation_unit") != "mm" {
			t.Errorf("precipitation_unit = %q", q.Get("precipitation_unit"))
		}
	})

	t.Run("array missing", func(t *testing.T) {
		pts := forecast(t, serveHourly(t, map[string]any{
			"time":           times,
			"temperature_2m": []any{1.0, 2.0, 3.0, 4.0},
		}, nil))
		for i, p := range pts {
			if p.PrecipMM != nil {
				t.Errorf("point %d: PrecipMM = %v, want nil", i, *p.PrecipMM)
			}
			if p.TempC == nil {
				t.Errorf("point %d: other fields must still parse", i)
			}
		}
	})

	t.Run("array shorter than time", func(t *testing.T) {
		pts := forecast(t, serveHourly(t, map[string]any{
			"time":          times,
			"precipitation": []any{0.4},
		}, nil))
		if pts[0].PrecipMM == nil || *pts[0].PrecipMM != 0.4 || pts[3].PrecipMM != nil {
			t.Errorf("short array: %v / %v", pts[0].PrecipMM, pts[3].PrecipMM)
		}
	})
}

func fp(v float64) *float64 { return &v }

func TestBuildRowsPastWindow(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	to := now.Add(ForecastDays * 24 * time.Hour)
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	pt := func(h int, mm *float64) Point { return Point{TS: at(h), PrecipMM: mm, TempC: fp(5)} }

	t.Run("future only keeps the old window", func(t *testing.T) {
		rows, from := buildRows(1, []Point{pt(0, nil), pt(1, nil)}, now, to, "open-meteo", now)
		if !from.Equal(now) || len(rows) != 2 {
			t.Fatalf("from=%v rows=%d", from, len(rows))
		}
	})

	t.Run("past hours with data extend the window", func(t *testing.T) {
		var pts []Point
		for h := -60; h <= 2; h++ {
			pts = append(pts, pt(h, fp(0.2)))
		}
		rows, from := buildRows(1, pts, now, to, "open-meteo", now)
		if want := at(-PastHours); !from.Equal(want) {
			t.Fatalf("from = %v, want %v (never older than PastHours)", from, want)
		}
		if len(rows) != PastHours+3 {
			t.Fatalf("rows = %d, want %d", len(rows), PastHours+3)
		}
		for _, r := range rows {
			if r.TS.Before(from) || r.PrecipMM == nil {
				t.Fatalf("bad row %+v", r)
			}
		}
	})

	t.Run("null past hours do not wipe history", func(t *testing.T) {
		var pts []Point
		for h := -48; h < 0; h++ {
			pts = append(pts, Point{TS: at(h)}) // provider returned the hour, all fields null
		}
		pts = append(pts, pt(0, fp(0)))
		rows, from := buildRows(1, pts, now, to, "open-meteo", now)
		if !from.Equal(now) || len(rows) != 1 {
			t.Fatalf("from=%v rows=%d, want window at now with 1 row", from, len(rows))
		}
	})

	t.Run("gap stops the past run", func(t *testing.T) {
		pts := []Point{pt(-5, fp(1)), pt(-4, fp(1)), {TS: at(-3)}, pt(-2, fp(1)), pt(-1, fp(1)), pt(0, nil)}
		rows, from := buildRows(1, pts, now, to, "open-meteo", now)
		if !from.Equal(at(-2)) || len(rows) != 3 {
			t.Fatalf("from=%v rows=%d, want from now-2h and 3 rows", from, len(rows))
		}
	})
}

// recordingRepo captures ReplaceRows calls for the Refresh test.
type recordingRepo struct {
	mu    sync.Mutex
	froms []time.Time
	rows  [][]Row
}

func (r *recordingRepo) DistinctCells(context.Context) ([]int64, error) {
	id, err := geo.CellIDFromLatLng(52.36, 4.87)
	return []int64{id}, err
}

func (r *recordingRepo) ReplaceRows(_ context.Context, _ int64, from, _ time.Time, rows []Row) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.froms = append(r.froms, from)
	r.rows = append(r.rows, rows)
	return nil
}
func (r *recordingRepo) DeleteBefore(context.Context, time.Time) error { return nil }
func (r *recordingRepo) ReserveQuota(context.Context, string, int, int) (bool, error) {
	return true, nil
}

type stubProvider struct {
	from   time.Time
	points func(from, to time.Time) []Point
}

func (s *stubProvider) Name() string { return "open-meteo" }
func (s *stubProvider) Forecast(_ context.Context, _, _ float64, from, to time.Time) ([]Point, error) {
	s.from = from
	return s.points(from, to), nil
}

func TestRefreshRequestsPastHoursAndStoresPrecip(t *testing.T) {
	prov := &stubProvider{points: func(from, to time.Time) []Point {
		var pts []Point
		for ts := from; !ts.After(to); ts = ts.Add(time.Hour) {
			pts = append(pts, Point{TS: ts, TempC: fp(10), PrecipMM: fp(0.5)})
		}
		return pts
	}}
	repo := &recordingRepo{}
	if _, err := NewService(repo, prov, 0).Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	if d := now.Sub(prov.from); d != PastHours*time.Hour && d != (PastHours+1)*time.Hour {
		t.Errorf("provider asked from %v, want now-%dh", prov.from, PastHours)
	}
	if len(repo.froms) != 1 {
		t.Fatalf("ReplaceRows calls = %d, want 1", len(repo.froms))
	}
	if !repo.froms[0].Equal(prov.from) {
		t.Errorf("replace window from %v, want %v", repo.froms[0], prov.from)
	}
	for _, r := range repo.rows[0] {
		if r.PrecipMM == nil || *r.PrecipMM != 0.5 {
			t.Fatalf("row %v lost precip_mm", r.TS)
		}
	}
}
