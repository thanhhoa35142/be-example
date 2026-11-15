package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenMeteoSendsModelsParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hourly": map[string]any{
				"time":                      []string{"2026-01-01T00:00"},
				"temperature_2m":            []any{10.0},
				"apparent_temperature":      []any{8.0},
				"precipitation_probability": []any{20},
				"wind_speed_10m":            []any{12.0},
				"uv_index":                  []any{1.0},
				"cloud_cover":               []any{50},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenMeteo(srv.Client(), srv.URL+"/v1/forecast", "gfs_global")
	pts, err := p.Forecast(context.Background(), 52.0, 4.0,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1", len(pts))
	}
	if pts[0].PrecipProb == nil || *pts[0].PrecipProb != 20 {
		t.Fatalf("PrecipProb = %v, want 20", pts[0].PrecipProb)
	}
	if !strings.Contains(gotQuery, "models=gfs_global") {
		t.Fatalf("query missing models=gfs_global: %s", gotQuery)
	}
}

func TestOpenMeteoOmitsModelsWhenEmpty(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"hourly": map[string]any{"time": []string{}}})
	}))
	defer srv.Close()

	p := NewOpenMeteo(srv.Client(), srv.URL+"/v1/forecast", "")
	if p.baseURL != srv.URL+"/v1/forecast" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	if NewOpenMeteo(nil, "", "").baseURL != openMeteoDefaultURL {
		t.Fatal("empty baseURL should default to public API")
	}
	if _, err := p.Forecast(context.Background(), 0, 0, time.Now(), time.Now()); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if strings.Contains(gotQuery, "models=") {
		t.Fatalf("unexpected models param: %s", gotQuery)
	}
}
