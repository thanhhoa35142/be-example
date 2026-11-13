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
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type fakeRecRepo struct{ got *spots.Filter }

func (f *fakeRecRepo) Nearby(_ context.Context, _, _, _ float64, _ time.Time, _ int, flt spots.Filter) ([]recommendations.Item, error) {
	f.got = &flt
	return nil, nil
}

func TestRecommendationsFilterParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		query    string
		wantCode int
	}{
		{"", 200},
		{"&kinds=park,beach&amenities=toilet,playground&minScore=40", 200},
		{"&kinds=&amenities=&minScore=", 200},
		{"&kinds=forest", 400},
		{"&amenities=toilet,sauna", 400},
		{"&minScore=101", 400},
		{"&minScore=-1", 400},
		{"&minScore=x", 400},
	}
	for _, tc := range cases {
		repo := &fakeRecRepo{}
		h := NewRecommendationsHandler(recommendations.NewService(repo))
		r := gin.New()
		r.GET("/v1/recommendations", h.List)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/recommendations?lat=52.37&lon=4.89"+tc.query, nil))
		if w.Code != tc.wantCode {
			t.Errorf("%q: status %d, want %d (%s)", tc.query, w.Code, tc.wantCode, w.Body)
		}
		if tc.wantCode == 400 {
			if !strings.Contains(w.Body.String(), "invalid_query") {
				t.Errorf("%q: want invalid_query, got %s", tc.query, w.Body)
			}
			if repo.got != nil {
				t.Errorf("%q: repo must not be called on invalid filter", tc.query)
			}
		}
	}

	repo := &fakeRecRepo{}
	h := NewRecommendationsHandler(recommendations.NewService(repo))
	r := gin.New()
	r.GET("/v1/recommendations", h.List)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/recommendations?lat=1&lon=2&kinds=park&amenities=toilet&minScore=40", nil))
	if repo.got == nil || len(repo.got.Kinds) != 1 || len(repo.got.Amenities) != 1 || *repo.got.MinScore != 40 {
		t.Fatalf("filter not forwarded: %+v", repo.got)
	}
}

func TestParseListQueryFilter(t *testing.T) {
	const bbox = "bbox=4.85,52.35,4.90,52.37"
	id := uuid.NewString()
	cases := []struct {
		query, wantCode string
	}{
		{bbox + "&kinds=park&amenities=toilet,water&minScore=40", ""},
		{"ids=" + id + "&kinds=park,beach&minScore=0", ""},
		{bbox + "&kinds=&amenities=&minScore=", ""},
		{bbox + "&kinds=forest", "invalid_query"},
		{"ids=" + id + "&amenities=sauna", "invalid_query"},
		{bbox + "&minScore=101", "invalid_query"},
		{"ids=" + id + "&minScore=abc", "invalid_query"},
	}
	for _, tc := range cases {
		v, _ := url.ParseQuery(tc.query)
		q, perr := parseListQuery(v)
		switch {
		case tc.wantCode == "" && perr != nil:
			t.Errorf("%q: unexpected %+v", tc.query, perr)
		case tc.wantCode != "" && (perr == nil || perr.code != tc.wantCode):
			t.Errorf("%q: want %s, got %+v", tc.query, tc.wantCode, perr)
		}
		if tc.wantCode == "" && strings.Contains(tc.query, "minScore=40") &&
			(q.Filter.MinScore == nil || *q.Filter.MinScore != 40 || len(q.Filter.Amenities) != 2) {
			t.Errorf("%q: filter not parsed: %+v", tc.query, q.Filter)
		}
	}
}
