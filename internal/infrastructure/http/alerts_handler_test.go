package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

// memAlertsRepo embeds the Repository port; only rule CRUD is implemented.
type memAlertsRepo struct {
	alerts.Repository
	rules       map[uuid.UUID]alerts.Rule
	missingSpot *uuid.UUID // SpotActive reports false for this spot
}

func (r *memAlertsRepo) DeviceExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (r *memAlertsRepo) SpotActive(_ context.Context, id uuid.UUID) (bool, error) {
	return r.missingSpot == nil || id != *r.missingSpot, nil
}
func (r *memAlertsRepo) GetRule(_ context.Context, id uuid.UUID) (*alerts.Rule, error) {
	if v, ok := r.rules[id]; ok {
		return &v, nil
	}
	return nil, alerts.ErrNotFound
}
func (r *memAlertsRepo) ListRules(context.Context, uuid.UUID) ([]alerts.Rule, error) {
	out := []alerts.Rule{}
	for _, v := range r.rules {
		out = append(out, v)
	}
	return out, nil
}
func (r *memAlertsRepo) InsertRuleLimited(_ context.Context, dev uuid.UUID, in alerts.RuleInput, s, e time.Duration, max int) (uuid.UUID, error) {
	if max >= 0 && len(r.rules) >= max {
		return uuid.Nil, alerts.ErrPlanLimit
	}
	id := uuid.New()
	r.rules[id] = alerts.Rule{ID: id, DeviceID: dev, SpotID: in.SpotID, H3Cell: in.H3Cell, RadiusM: in.RadiusM,
		MinScore: in.MinScore, DaysMask: in.DaysMask, StartLocal: s, EndLocal: e, TZ: in.TZ, Enabled: true}
	return id, nil
}

func TestAlertsHandlerArea(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev := uuid.New()
	repo := &memAlertsRepo{rules: map[uuid.UUID]alerts.Rule{}}
	h := NewAlertsHandler(alerts.NewService(repo, planMap{dev: "plus"}, nil))
	r := gin.New()
	r.POST("/v1/alerts", h.Create)
	r.GET("/v1/alerts", h.List)

	post := func(extra string) *httptest.ResponseRecorder {
		body := `{"deviceId":"` + dev.String() + `","minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"Europe/Amsterdam",` + extra + `}`
		req := httptest.NewRequest("POST", "/v1/alerts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := post(`"area":{"lat":52.3612,"lon":4.8791},"radiusM":4000`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	c, ok := got.Data["areaCenter"].(map[string]any)
	if !ok || got.Data["h3Cell"] == nil || got.Data["spotId"] != nil || got.Data["radiusM"] != float64(4000) {
		t.Fatalf("bad area response: %s", w.Body)
	}
	if lat := c["lat"].(float64); lat < 52.3 || lat > 52.4 {
		t.Fatalf("center lat %v", lat)
	}
	if strings.Contains(w.Body.String(), "52.3612") || strings.Contains(w.Body.String(), "4.8791") {
		t.Fatal("raw coordinates leaked")
	}

	spot := uuid.New().String()
	w = post(`"spotId":"` + spot + `"`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"areaCenter":null`) {
		t.Fatalf("spot rule: %d %s", w.Code, w.Body)
	}

	for name, extra := range map[string]string{
		"spot+area":   `"spotId":"` + spot + `","area":{"lat":52.3,"lon":4.8},"radiusM":1000`,
		"missing lon": `"area":{"lat":52.3},"radiusM":1000`,
		"oor":         `"area":{"lat":95,"lon":4.8},"radiusM":1000`,
		"no radius":   `"area":{"lat":52.3,"lon":4.8}`,
		"nothing":     `"radiusM":1000`,
	} {
		if w := post(extra); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_body"`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}

	// Free plan: area rules count toward the single-rule cap.
	free := uuid.New()
	h2 := NewAlertsHandler(alerts.NewService(&memAlertsRepo{rules: map[uuid.UUID]alerts.Rule{uuid.New(): {}}}, planMap{free: "free"}, nil))
	r2 := gin.New()
	r2.POST("/v1/alerts", h2.Create)
	body := `{"deviceId":"` + free.String() + `","area":{"lat":52.36,"lon":4.88},"radiusM":3000,"minScore":50,"daysMask":127,"startLocal":"08:00","endLocal":"10:00","tz":"UTC"}`
	req := httptest.NewRequest("POST", "/v1/alerts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, req)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "plan_limit") {
		t.Fatalf("free cap: %d %s", w.Code, w.Body)
	}
}

func TestAlertsHandlerValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev := uuid.New()
	missing := uuid.New()
	repo := &memAlertsRepo{rules: map[uuid.UUID]alerts.Rule{}, missingSpot: &missing}
	h := NewAlertsHandler(alerts.NewService(repo, planMap{dev: "plus"}, nil))
	r := gin.New()
	r.POST("/v1/alerts", h.Create)
	r.GET("/v1/alerts", h.List)
	r.DELETE("/v1/alerts/:id", h.Delete)

	post := func(start, end, tz, spot string) *httptest.ResponseRecorder {
		body := `{"deviceId":"` + dev.String() + `","spotId":"` + spot + `","minScore":50,"daysMask":127,"startLocal":"` + start + `","endLocal":"` + end + `","tz":"` + tz + `"}`
		req := httptest.NewRequest("POST", "/v1/alerts", strings.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	ok := uuid.New().String()
	for name, c := range map[string]struct{ s, e, tz string }{
		"garbage start": {"nope", "10:00", "UTC"},
		"garbage end":   {"08:00", "25:99", "UTC"},
		"equal":         {"08:00", "08:00", "UTC"},
		"empty tz":      {"08:00", "10:00", ""},
		"Local tz":      {"08:00", "10:00", "Local"},
		"unknown tz":    {"08:00", "10:00", "Mars/Base"},
	} {
		if w := post(c.s, c.e, c.tz, ok); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_body"`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := post("22:00", "06:00", "UTC", ok); w.Code != http.StatusCreated {
		t.Errorf("overnight window must be valid: %d %s", w.Code, w.Body)
	}
	if w := post("08:00", "10:00", "UTC", missing.String()); w.Code != 404 {
		t.Errorf("missing spot: want 404, got %d %s", w.Code, w.Body)
	}

	// Device id: header preferred, legacy query still accepted, neither -> 400.
	get := func(hdr, query string) int {
		req := httptest.NewRequest("GET", "/v1/alerts"+query, nil)
		if hdr != "" {
			req.Header.Set("X-Device-ID", hdr)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := get(dev.String(), ""); c != 200 {
		t.Errorf("header: %d", c)
	}
	if c := get("", "?deviceId="+dev.String()); c != 200 {
		t.Errorf("legacy query: %d", c)
	}
	if c := get("not-a-uuid", "?deviceId="+dev.String()); c != 400 {
		t.Errorf("bad header must not fall back to query: %d", c)
	}
	if c := get("", ""); c != 400 {
		t.Errorf("missing id: %d", c)
	}
}
