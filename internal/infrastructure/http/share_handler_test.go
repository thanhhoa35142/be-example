package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

func TestAppleAppSiteAssociation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	serve := func(opts ShareOptions) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/.well-known/apple-app-site-association", NewShareHandler(nil, opts).AppleAppSiteAssociation)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil))
		return w
	}

	if w := serve(ShareOptions{}); w.Code != http.StatusNotFound {
		t.Fatalf("unconfigured: got %d, want 404", w.Code)
	}
	w := serve(ShareOptions{AppleAppIDs: []string{"TEAM123456.app.example"}})
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("configured: got %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{`"TEAM123456.app.example"`, `"/s/*"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body missing %s: %s", want, w.Body.String())
		}
	}
}

func TestShareTitleAndTemplateEscaping(t *testing.T) {
	name, city := `<script>alert(1)</script>`, "Utrecht"
	named := spots.SpotDetail{Spot: spots.Spot{Kind: "park", Name: &name, City: &city}}
	if got := shareSubtitle(named); got != "Park · Utrecht" {
		t.Errorf("subtitle = %q", got)
	}
	unnamed := spots.SpotDetail{Spot: spots.Spot{Kind: "site", City: &city}}
	if got := shareTitle(unnamed); got != "Picnic area · Utrecht" {
		t.Errorf("unnamed title = %q", got)
	}
	if got := shareSubtitle(unnamed); got != "" {
		t.Errorf("unnamed subtitle = %q", got)
	}

	var b strings.Builder
	if err := shareTemplate.Execute(&b, sharePage{Title: shareTitle(named), HasScore: true, Score: 82, Tier: scoreTier(82)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "<script>alert") {
		t.Error("spot name was not HTML-escaped")
	}
	if !strings.Contains(b.String(), "<b>82</b><span>Great</span>") {
		t.Error("score not rendered")
	}
}
