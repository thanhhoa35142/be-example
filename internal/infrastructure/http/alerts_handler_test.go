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
	rules map[uuid.UUID]alerts.Rule
}

func (r *memAlertsRepo) DeviceExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (r *memAlertsRepo) CountRules(context.Context, uuid.UUID) (int, error)    { return len(r.rules), nil }
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
func (r *memAlertsRepo) InsertRule(_ context.Context, dev uuid.UUID, in alerts.RuleInput, s, e time.Duration) (uuid.UUID, error) {
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
