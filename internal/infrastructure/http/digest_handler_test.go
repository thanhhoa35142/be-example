package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

type memDigestRepo struct {
	known map[uuid.UUID]uuid.UUID // requested -> resolved
	rows  map[uuid.UUID]*digest.Digest
}

func (r *memDigestRepo) Upsert(_ context.Context, id uuid.UUID, ids []uuid.UUID, en bool) (*digest.Digest, error) {
	d := &digest.Digest{DeviceID: id, SpotIDs: ids, Enabled: en, SendDow: 4, SendHour: 18}
	r.rows[id] = d
	return d, nil
}
func (r *memDigestRepo) Get(_ context.Context, id uuid.UUID) (*digest.Digest, error) {
	if d, ok := r.rows[id]; ok {
		return d, nil
	}
	return nil, digest.ErrNotFound
}
func (r *memDigestRepo) Delete(_ context.Context, id uuid.UUID) error { delete(r.rows, id); return nil }
func (r *memDigestRepo) DeviceExists(_ context.Context, id uuid.UUID) (bool, error) {
	return id != uuid.Nil && id.String() != "00000000-0000-0000-0000-000000000001", nil
}
func (r *memDigestRepo) ResolveSpots(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	for _, id := range ids {
		if t, ok := r.known[id]; ok {
			out[id] = t
		}
	}
	return out, nil
}
func (r *memDigestRepo) ListCandidates(context.Context) ([]digest.Candidate, error) { return nil, nil }
func (r *memDigestRepo) Claim(context.Context, uuid.UUID, time.Time, *time.Time) (bool, error) {
	return false, nil
}
func (r *memDigestRepo) Release(context.Context, uuid.UUID, time.Time, *time.Time) error { return nil }
func (r *memDigestRepo) Spots(context.Context, []uuid.UUID) ([]digest.SpotInfo, error) {
	return nil, nil
}
func (r *memDigestRepo) Scores(context.Context, uuid.UUID, time.Time, time.Time) ([]digest.HourScore, error) {
	return nil, nil
}

type planMap map[uuid.UUID]string

func (p planMap) Plan(_ context.Context, id uuid.UUID) (string, error) { return p[id], nil }

func TestDigestHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plus, free := uuid.New(), uuid.New()
	a, b, merged := uuid.New(), uuid.New(), uuid.New()
	repo := &memDigestRepo{known: map[uuid.UUID]uuid.UUID{a: a, b: b, merged: a}, rows: map[uuid.UUID]*digest.Digest{}}
	h := NewDigestHandler(digest.NewService(repo, planMap{plus: "plus", free: "free"}, nil, nil))
	r := gin.New()
	r.PUT("/v1/devices/:id/digest", h.Put)
	r.GET("/v1/devices/:id/digest", h.Get)
	r.DELETE("/v1/devices/:id/digest", h.Delete)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int, sub string) {
		t.Helper()
		if w.Code != status || !strings.Contains(w.Body.String(), sub) {
			t.Fatalf("got %d %s, want %d containing %q", w.Code, w.Body.String(), status, sub)
		}
	}
	p := "/v1/devices/" + plus.String() + "/digest"

	check(do("GET", p, ""), 404, `"code":"not_found"`)
	check(do("PUT", p, `{"spotIds":["`+merged.String()+`","`+b.String()+`"],"enabled":true}`), 200,
		`{"data":{"enabled":true,"sendDow":4,"sendHour":18,"spotIds":["`+a.String()+`","`+b.String()+`"]}}`)
	check(do("GET", p, ""), 200, `"sendHour":18`)
	check(do("PUT", "/v1/devices/"+free.String()+"/digest", `{"spotIds":["`+a.String()+`"],"enabled":true}`), 403, `"code":"plan_limit"`)
	check(do("PUT", p, `{"spotIds":["`+uuid.NewString()+`"],"enabled":true}`), 400, `"code":"invalid_body"`)
	check(do("PUT", p, `{"spotIds":["nope"],"enabled":true}`), 400, `"code":"invalid_body"`)
	check(do("PUT", p, `{"enabled":true}`), 400, `"code":"invalid_body"`)
	check(do("PUT", p, `not json`), 400, `"code":"invalid_body"`)
	many := make([]string, 21)
	for i := range many {
		many[i] = `"` + a.String() + `"`
	}
	check(do("PUT", p, `{"spotIds":[`+strings.Join(many, ",")+`],"enabled":true}`), 400, `"code":"invalid_body"`)
	check(do("PUT", "/v1/devices/not-a-uuid/digest", `{}`), 400, `"code":"invalid_id"`)
	check(do("PUT", "/v1/devices/00000000-0000-0000-0000-000000000001/digest", `{"spotIds":[],"enabled":false}`), 404, `"code":"not_found"`)
	check(do("DELETE", p, ""), 204, "")
	check(do("DELETE", p, ""), 204, "")
	check(do("GET", p, ""), 404, `"code":"not_found"`)
	_ = http.StatusOK
}
