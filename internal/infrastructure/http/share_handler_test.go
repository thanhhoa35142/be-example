package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	for _, want := range []string{`"TEAM123456.app.example"`, `"/s/*"`, `"/p/*"`} {
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

// shareFakeRepo is a spots.Repository for the share page: one active spot
// (or a merged one), with a score per requested hour.
type shareFakeRepo struct {
	detail spots.SpotDetail
	score  *spots.ScoreSnapshot
	whens  []*time.Time
}

func (f *shareFakeRepo) List(_ context.Context, q spots.ListQuery, _, _, _ time.Time, _ int) ([]spots.SpotWithScore, int, error) {
	f.whens = append(f.whens, q.When)
	return []spots.SpotWithScore{{Spot: f.detail.Spot, Score: f.score}}, 1, nil
}
func (f *shareFakeRepo) WeatherProvider(context.Context, int64) (string, error) { return "", nil }
func (f *shareFakeRepo) ListPacks(context.Context) ([]spots.Pack, error)        { return nil, nil }
func (f *shareFakeRepo) GetByID(_ context.Context, id uuid.UUID) (spots.SpotDetail, error) {
	if id != f.detail.ID {
		return spots.SpotDetail{}, spots.ErrNotFound
	}
	return f.detail, nil
}
func (f *shareFakeRepo) ListAmenities(context.Context, uuid.UUID) ([]spots.Amenity, error) {
	return nil, nil
}
func (f *shareFakeRepo) ListSources(context.Context, uuid.UUID) ([]spots.Source, error) {
	return nil, nil
}
func (f *shareFakeRepo) ListForecast(context.Context, uuid.UUID, int64, time.Time, time.Time) ([]spots.ForecastPoint, error) {
	return nil, nil
}

func newShareFake() *shareFakeRepo {
	name := "Vondelpark"
	score := 82
	return &shareFakeRepo{
		detail: spots.SpotDetail{
			Spot:   spots.Spot{ID: uuid.New(), Kind: "park", Name: &name, TZ: "Europe/Amsterdam", Lat: 52.358, Lon: 4.868},
			Status: "active",
		},
		score: &spots.ScoreSnapshot{Score: &score, Reasons: []spots.Reason{
			{Code: "weather.clear", Params: map[string]any{"temp": 22.4}},
			{Code: "crowd.low"},
			{Code: "unknown.code"},
			{Code: "fresh.recent"},
			{Code: "amenity.well_equipped"},
		}},
	}
}

func serveShare(repo *shareFakeRepo, target string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s/:id", NewShareHandler(spots.NewService(repo), ShareOptions{}).Spot)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

const canonAt = "2006-01-02T15:04:05Z"

func TestShareSpotValidAt(t *testing.T) {
	repo := newShareFake()
	at := time.Now().UTC().Truncate(time.Hour).Add(48 * time.Hour)
	canon := at.Format(canonAt)
	w := serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+canon)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(repo.whens) != 1 || repo.whens[0] == nil || !repo.whens[0].Equal(at) {
		t.Fatalf("score not queried for the planned hour: %v", repo.whens)
	}
	body := w.Body.String()
	local := at.In(mustLoc(t, "Europe/Amsterdam"))
	for _, want := range []string{
		`<meta name="robots" content="noindex">`,
		`<meta property="og:title" content="Picnic on ` + local.Format("Monday 2 Jan, 15:04") + ` · Vondelpark">`,
		`<p class="when">` + local.Format("Monday 2 January · 15:04") + `</p>`,
		"Picnic score at " + local.Format("15:04"),
		"<b>82</b>",
		"<li>Pleasant, 22°C</li><li>Quiet</li><li>Recently verified</li></ul>",
		`href="pickhour://spot/` + repo.detail.ID.String() + `?at=` + url.QueryEscape(canon) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "Well equipped") {
		t.Error("more than 3 reasons rendered")
	}
	if strings.Contains(body, "better times") {
		t.Error("open-app hint shown for a good score")
	}
	if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestShareSpotNonCanonicalAtRedirects(t *testing.T) {
	repo := newShareFake()
	at := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Hour)
	raw := at.Add(17 * time.Minute).In(time.FixedZone("", 2*3600)).Format(time.RFC3339)
	w := serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+url.QueryEscape(raw))
	if w.Code != http.StatusFound {
		t.Fatalf("status %d, want 302", w.Code)
	}
	want := "/s/" + repo.detail.ID.String() + "?at=" + url.QueryEscape(at.Format(canonAt))
	if got := w.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got := w.Header().Get("Cache-Control"); !strings.HasPrefix(got, "private") {
		t.Errorf("redirect Cache-Control = %q, want private", got)
	}
}

func TestShareSpotInvalidAtRendersPlainPage(t *testing.T) {
	now := time.Now().UTC()
	cases := map[string]string{
		"garbage":       "tomorrow",
		"date only":     now.Format("2006-01-02"),
		"too long":      strings.Repeat("9", 200),
		"past":          now.Add(-3 * time.Hour).Format(time.RFC3339),
		"beyond 7 days": now.Add(8 * 24 * time.Hour).Format(time.RFC3339),
		"year 9999":     "9999-01-01T00:00:00Z",
		"empty":         "",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newShareFake()
			w := serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+url.QueryEscape(raw))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, "Picnic score right now") || strings.Contains(body, `<ul class="reasons">`) {
				t.Error("not the plain spot page")
			}
			if len(repo.whens) != 1 || repo.whens[0] != nil {
				t.Errorf("score queried with at: %v", repo.whens)
			}
			if !strings.Contains(body, `href="pickhour://spot/`+repo.detail.ID.String()+`"`) {
				t.Error("app URL should carry no at")
			}
			if !strings.Contains(body, `content="noindex"`) {
				t.Error("missing noindex")
			}
			if got := w.Header().Get("Cache-Control"); !strings.HasPrefix(got, "private") {
				t.Errorf("Cache-Control = %q, want private", got)
			}
		})
	}
}

func TestShareSpotNoAtUnchanged(t *testing.T) {
	repo := newShareFake()
	w := serveShare(repo, "/s/"+repo.detail.ID.String())
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, "noindex") || w.Header().Get("X-Robots-Tag") != "" {
		t.Fatalf("plain page changed: %d", w.Code)
	}
	if !strings.Contains(body, `<meta property="og:title" content="Vondelpark">`) ||
		!strings.Contains(body, "Picnic score right now: 82 (Great)") {
		t.Error("plain page OG tags changed")
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestShareSpotAtWithoutScoreOrPoorScore(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Hour).Format(canonAt)

	repo := newShareFake()
	repo.score = nil
	w := serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+at)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `class="score"`) ||
		!strings.Contains(w.Body.String(), "Picnic planned for") {
		t.Errorf("no-score page: %d", w.Code)
	}

	repo = newShareFake()
	low, rain := 20, "rain"
	repo.score = &spots.ScoreSnapshot{Score: &low, CapReason: &rain, Reasons: []spots.Reason{{Code: "cap.rain"}}}
	w = serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+at)
	body := w.Body.String()
	if !strings.Contains(body, "<li>Rain expected</li>") || !strings.Contains(body, "Open Pickhour to see better times.") {
		t.Error("poor-score page missing reason or open-app hint")
	}
}

func TestShareSpotMergedRedirectKeepsAt(t *testing.T) {
	repo := newShareFake()
	newID := uuid.New()
	repo.detail.Status = "merged"
	repo.detail.MergedInto = &newID
	at := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour).Format(canonAt)

	w := serveShare(repo, "/s/"+repo.detail.ID.String()+"?at="+at)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, want 301", w.Code)
	}
	if got, want := w.Header().Get("Location"), "/s/"+newID.String()+"?at="+url.QueryEscape(at); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	// An invalid at is dropped, not forwarded.
	w = serveShare(repo, "/s/"+repo.detail.ID.String()+"?at=junk")
	if got, want := w.Header().Get("Location"), "/s/"+newID.String(); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
