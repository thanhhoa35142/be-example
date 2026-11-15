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
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type plansFake struct{ shared *plans.Shared }

func (f plansFake) Spot(context.Context, uuid.UUID) (*plans.SpotRef, error) {
	return &plans.SpotRef{TZ: "UTC"}, nil
}
func (plansFake) InsertLimited(context.Context, plans.NewPlan, int) (uuid.UUID, error) {
	return uuid.Nil, plans.ErrPlanLimit
}
func (plansFake) ListForDevice(context.Context, uuid.UUID) ([]plans.Plan, error) { return nil, nil }
func (plansFake) Cancel(context.Context, uuid.UUID, uuid.UUID) (bool, error)     { return false, nil }
func (f plansFake) ActiveByToken(context.Context, string) (*plans.Shared, error) {
	if f.shared == nil {
		return nil, plans.ErrNotFound
	}
	c := *f.shared
	return &c, nil
}
func (plansFake) Follow(context.Context, string, uuid.UUID, int, int) (uuid.UUID, error) {
	return uuid.Nil, plans.ErrOwnPlan
}
func (plansFake) Unfollow(context.Context, string, uuid.UUID) error { return nil }
func (plansFake) WindowScores(context.Context, uuid.UUID, time.Time, time.Time) ([]plans.HourScore, error) {
	return []plans.HourScore{{Score: 72, Reasons: []spots.Reason{{Code: "a"}, {Code: "b"}, {Code: "c"}, {Code: "d"}}}}, nil
}

func plansRouter(repo plansFake) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewPlansHandler(plans.NewService(repo, nil, plans.DefaultConfig()))
	r := gin.New()
	r.GET("/v1/shared-plans/:token", h.Shared)
	r.POST("/v1/plans", h.Create)
	r.POST("/v1/plan-follows/:token", h.Follow)
	r.DELETE("/v1/plans/:id", h.Cancel)
	return r
}

func TestSharedPlanView(t *testing.T) {
	start := time.Now().Add(time.Hour).UTC()
	r := plansRouter(plansFake{shared: &plans.Shared{SpotID: uuid.New(), SpotName: "Park", SpotKind: "park",
		Start: start, End: start.Add(2 * time.Hour), TZ: "UTC"}})
	tok, _ := plans.NewShareToken()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/shared-plans/"+tok, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "private, max-age=60" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("headers %v", w.Header())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, k := range []string{"id", "deviceId", "shareToken", "followerCount", "baselineScore", "alternative"} {
		if _, ok := body.Data[k]; ok {
			t.Errorf("shared view leaks %q", k)
		}
	}
	if n := len(body.Data["reasons"].([]any)); n != 3 {
		t.Errorf("reasons %d, want 3", n)
	}
	if body.Data["score"].(float64) != 72 || body.Data["state"] != "ok" {
		t.Errorf("data %v", body.Data)
	}
}

func TestSharedPlanNotFoundIsUniform(t *testing.T) {
	r := plansRouter(plansFake{})
	tok, _ := plans.NewShareToken()
	for _, p := range []string{"/v1/shared-plans/" + tok, "/v1/shared-plans/short", "/v1/shared-plans/%2E%2E"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"not_found"`) {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
		if !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Errorf("%s: missing noindex on 404", p)
		}
	}
}

func TestPlanErrorCodes(t *testing.T) {
	r := plansRouter(plansFake{})
	dev := uuid.NewString()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(DeviceIDHeader, dev)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	start := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	spot := uuid.NewString()
	cases := []struct {
		w    *httptest.ResponseRecorder
		code int
		rc   string
	}{
		{do("POST", "/v1/plans", `{"spotId":"`+spot+`","start":"`+start+`","end":"`+end+`"}`), 403, "plan_limit"},
		{do("POST", "/v1/plans", `{"spotId":"`+spot+`","start":"`+past+`","end":"`+end+`"}`), 400, "plan_start_past"},
		{do("POST", "/v1/plans", `{"spotId":"`+spot+`","start":"`+start+`","end":"`+end+`","tz":"Nope/Nope"}`), 400, "invalid_tz"},
		{do("POST", "/v1/plans", `{"spotId":"`+spot+`"}`), 400, "invalid_body"},
		{do("POST", "/v1/plan-follows/AAAAAAAAAAAAAAAAAAAAAA", ""), 409, "own_plan"},
		{do("POST", "/v1/plan-follows/bad", ""), 404, "not_found"},
		{do("DELETE", "/v1/plans/"+uuid.NewString(), ""), 404, "not_found"},
	}
	for i, c := range cases {
		if c.w.Code != c.code || !strings.Contains(c.w.Body.String(), `"`+c.rc+`"`) {
			t.Errorf("case %d: %d %s, want %d %s", i, c.w.Code, c.w.Body, c.code, c.rc)
		}
	}
}
