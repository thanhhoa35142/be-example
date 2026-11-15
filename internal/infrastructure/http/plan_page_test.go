package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
)

func servePlanPage(repo plansFake, target string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := plans.NewService(repo, nil, plans.DefaultConfig())
	r.GET("/p/:token", NewShareHandler(nil, ShareOptions{Plans: svc}).PlanPage)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestPlanPage(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, ams)
	repo := plansFake{shared: &plans.Shared{
		SpotID: uuid.New(), SpotName: "Vondel <park>", SpotKind: "park", SpotCity: "Amsterdam",
		Lat: 52.358, Lon: 4.868, Start: start.UTC(), End: start.Add(3 * time.Hour).UTC(), TZ: "Europe/Amsterdam",
	}}
	tok, _ := plans.NewShareToken()
	w := servePlanPage(repo, "/p/"+tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Vondel &lt;park&gt;", "Park · Amsterdam", "Saturday 21 November · 10:00–13:00",
		"<b>72</b>", `content="noindex"`, `href="pickhour://plan/` + tok + `"`, `href="/p/` + tok + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "better times") {
		t.Error("OpenHint shown for an ok plan")
	}
	if w.Header().Get("Cache-Control") != "private, max-age=60" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("headers %v", w.Header())
	}
	if strings.Contains(body, repo.shared.SpotID.String()) {
		t.Error("page carries the spot id (not needed; keep the page minimal)")
	}
}

func TestPlanPageNotFound(t *testing.T) {
	tok, _ := plans.NewShareToken()
	for _, p := range []string{"/p/" + tok, "/p/short", "/p/%3Cscript%3E"} {
		w := servePlanPage(plansFake{}, p)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
			t.Errorf("%s: %d %v", p, w.Code, w.Header())
		}
		if strings.Contains(w.Body.String(), "script") {
			t.Errorf("%s: echoes input", p)
		}
	}
	// No plans service wired: still a plain 404.
	r := gin.New()
	r.GET("/p/:token", NewShareHandler(nil, ShareOptions{}).PlanPage)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p/"+tok, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unwired: %d", w.Code)
	}
}

func TestPlanPageBadStateShowsHint(t *testing.T) {
	base := 90
	start := time.Now().Add(24 * time.Hour).UTC()
	sh := (&plans.Shared{SpotKind: "beach", Start: start, End: start.Add(2 * time.Hour), TZ: "UTC"}).WithBaseline(&base)
	tok, _ := plans.NewShareToken()
	w := servePlanPage(plansFake{shared: sh}, "/p/"+tok)
	body := w.Body.String()
	// Window mean 72 against baseline 90 → worse.
	if !strings.Contains(body, "better times") || !strings.Contains(body, "<h1>Beach</h1>") {
		t.Fatalf("worse plan page: %s", body)
	}
}
