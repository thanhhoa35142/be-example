package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem body: %q", w.Body)
	}
	return p.Code
}

func do(r http.Handler, method, path, remote string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote + ":1234"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func ok(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) }

func TestRateLimitClassesAndExemptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), RateLimit(RateLimits{
		Read:   ratelimit.PerMinute(60, 3),
		Write:  ratelimit.PerMinute(60, 2),
		Device: ratelimit.PerMinute(60, 1),
	}))
	r.GET("/healthz", ok)
	r.GET("/readyz", ok)
	r.GET("/v1/x", ok)
	r.POST("/v1/x", ok)
	r.POST("/v1/devices", ok)

	for i := 0; i < 50; i++ { // health probes are never limited
		if w := do(r, "GET", "/healthz", "9.9.9.9", nil, ""); w.Code != 200 {
			t.Fatalf("healthz limited at %d", i)
		}
		if w := do(r, "GET", "/readyz", "9.9.9.9", nil, ""); w.Code != 200 {
			t.Fatalf("readyz limited at %d", i)
		}
	}
	for i := 0; i < 3; i++ {
		if w := do(r, "GET", "/v1/x", "1.1.1.1", nil, ""); w.Code != 200 {
			t.Fatalf("read %d: %d", i, w.Code)
		}
	}
	w := do(r, "GET", "/v1/x", "1.1.1.1", nil, "")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || problemCode(t, w) != "rate_limited" {
		t.Fatalf("read over budget: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("content type %q", w.Header().Get("Content-Type"))
	}
	// Other IPs and the write budget are independent.
	if w := do(r, "GET", "/v1/x", "2.2.2.2", nil, ""); w.Code != 200 {
		t.Fatalf("other ip: %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := do(r, "POST", "/v1/x", "1.1.1.1", nil, ""); w.Code != 200 {
			t.Fatalf("write %d: %d", i, w.Code)
		}
	}
	if w := do(r, "POST", "/v1/x", "1.1.1.1", nil, ""); w.Code != 429 {
		t.Fatalf("write over budget: %d", w.Code)
	}
	// POST /v1/devices has its own (tighter) bucket.
	if w := do(r, "POST", "/v1/devices", "3.3.3.3", nil, ""); w.Code != 200 {
		t.Fatalf("device 1: %d", w.Code)
	}
	if w := do(r, "POST", "/v1/devices", "3.3.3.3", nil, ""); w.Code != 429 {
		t.Fatalf("device 2 should be limited: %d", w.Code)
	}
	if w := do(r, "POST", "/v1/x", "3.3.3.3", nil, ""); w.Code != 200 {
		t.Fatalf("device limiter must not eat the write budget: %d", w.Code)
	}
}

func TestClientIPIgnoresSpoofedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(trusted []string) *gin.Engine {
		r, err := NewRouter(nil, Handlers{}, Options{
			TrustedProxies: trusted,
			Limits:         &RateLimits{Read: ratelimit.PerMinute(60, 2)},
		})
		if err != nil {
			t.Fatal(err)
		}
		r.GET("/v1/probe", ok)
		return r
	}
	// Default: no trusted proxy. Rotating X-Forwarded-For must not mint
	// fresh buckets.
	r := build(nil)
	for i := 0; i < 2; i++ {
		do(r, "GET", "/v1/probe", "4.4.4.4", map[string]string{"X-Forwarded-For": "10.0.0." + string(rune('1'+i))}, "")
	}
	if w := do(r, "GET", "/v1/probe", "4.4.4.4", map[string]string{"X-Forwarded-For": "10.9.9.9"}, ""); w.Code != 429 {
		t.Fatalf("spoofed XFF bypassed the limiter: %d", w.Code)
	}
	// When the peer IS a configured proxy, XFF identifies the client.
	r = build([]string{"4.4.4.4"})
	for i := 0; i < 2; i++ {
		do(r, "GET", "/v1/probe", "4.4.4.4", map[string]string{"X-Forwarded-For": "7.7.7.7"}, "")
	}
	if w := do(r, "GET", "/v1/probe", "4.4.4.4", map[string]string{"X-Forwarded-For": "7.7.7.7"}, ""); w.Code != 429 {
		t.Fatalf("same client behind trusted proxy not limited: %d", w.Code)
	}
	if w := do(r, "GET", "/v1/probe", "4.4.4.4", map[string]string{"X-Forwarded-For": "8.8.8.8"}, ""); w.Code != 200 {
		t.Fatalf("different client behind trusted proxy shared a bucket: %d", w.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), BodyLimit(100, map[string]int64{"/big": 1000}))
	echo := func(c *gin.Context) {
		var v map[string]any
		if err := c.ShouldBindJSON(&v); err != nil {
			c.JSON(400, gin.H{"err": "bind"})
			return
		}
		c.JSON(200, v)
	}
	r.POST("/small", echo)
	r.POST("/big", echo)

	pad := func(n int) string { return `{"p":"` + strings.Repeat("a", n) + `"}` }
	if w := do(r, "POST", "/small", "1.1.1.1", nil, pad(50)); w.Code != 200 {
		t.Fatalf("within limit: %d %s", w.Code, w.Body)
	}
	w := do(r, "POST", "/small", "1.1.1.1", nil, pad(200))
	if w.Code != 413 || problemCode(t, w) != "payload_too_large" {
		t.Fatalf("over limit: %d %s", w.Code, w.Body)
	}
	// Chunked/unknown length bodies are capped too.
	req := httptest.NewRequest("POST", "/small", io_NoLen(bytes.NewReader([]byte(pad(200)))))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("unknown length over limit: %d", rec.Code)
	}
	if w := do(r, "POST", "/big", "1.1.1.1", nil, pad(500)); w.Code != 200 {
		t.Fatalf("route override: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", "/big", "1.1.1.1", nil, pad(2000)); w.Code != 413 {
		t.Fatalf("route override still capped: %d", w.Code)
	}
}

type noLen struct{ r *bytes.Reader }

func (n noLen) Read(p []byte) (int, error) { return n.r.Read(p) }
func io_NoLen(r *bytes.Reader) noLen       { return noLen{r} }

func TestRequestIDSanitised(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.GET("/", ok)
	for name, in := range map[string]string{
		"too long": strings.Repeat("a", 65),
		"newline":  "abc\ndef",
		"spaces":   "a b",
		"empty":    "",
		"json-ish": `{"x":1}`,
		"unicode":  "café",
	} {
		got := do(r, "GET", "/", "1.1.1.1", map[string]string{"X-Request-ID": in}, "").Header().Get("X-Request-ID")
		if got == in || len(got) == 0 || !requestIDRe.MatchString(got) {
			t.Errorf("%s: echoed/produced %q", name, got)
		}
	}
	if got := do(r, "GET", "/", "1.1.1.1", map[string]string{"X-Request-ID": "req-123.abc_X"}, "").Header().Get("X-Request-ID"); got != "req-123.abc_X" {
		t.Errorf("safe id not kept: %q", got)
	}
}

func TestRecoveryReturnsProblem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), Recovery())
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })
	w := do(r, "GET", "/boom", "1.1.1.1", nil, "")
	var p map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 500 || p["code"] != "internal_error" || p["status"] != float64(500) || p["requestId"] == "" ||
		w.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(w.Body.String(), "kaboom") {
		t.Fatalf("recovery: %d %s %v", w.Code, w.Body, w.Header())
	}
}

func TestAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(token string) *gin.Engine {
		r := gin.New()
		r.Use(RequestID())
		g := r.Group("/admin", AdminAudit(), AdminAuth(token, ratelimit.New(ratelimit.PerMinute(60, 3))))
		g.GET("/x", ok)
		g.POST("/x", ok)
		return r
	}
	r := build("s3cret")
	auth := func(v string) map[string]string { return map[string]string{"Authorization": v} }

	if w := do(r, "GET", "/admin/x", "1.1.1.1", auth("Bearer s3cret"), ""); w.Code != 200 {
		t.Fatalf("valid token: %d", w.Code)
	}
	for _, h := range []string{"", "Bearer", "Bearer wrong", "s3cret", "Bearer s3cret ", "bearer s3cret"} {
		w := do(r, "GET", "/admin/x", "2.2.2.2", auth(h), "")
		if w.Code != 401 && w.Code != 429 {
			t.Errorf("header %q: %d", h, w.Code)
		}
	}
	// 3 failures burn the per-IP budget; then even the right token is 429
	// for that IP, while another IP is unaffected.
	w := do(r, "GET", "/admin/x", "2.2.2.2", auth("Bearer s3cret"), "")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || problemCode(t, w) != "rate_limited" {
		t.Fatalf("lockout: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/admin/x", "5.5.5.5", auth("Bearer s3cret"), ""); w.Code != 200 {
		t.Fatalf("other ip: %d", w.Code)
	}
	// Failure responses are problem JSON, not ad-hoc gin.H.
	w = do(r, "GET", "/admin/x", "6.6.6.6", auth("Bearer nope"), "")
	if w.Code != 401 || problemCode(t, w) != "unauthorized" || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("401 shape: %d %s", w.Code, w.Body)
	}
	// Disabled surface.
	if w := do(build(""), "GET", "/admin/x", "1.1.1.1", auth("Bearer "), ""); w.Code != 404 || problemCode(t, w) != "not_found" {
		t.Fatalf("disabled: %d %s", w.Code, w.Body)
	}
}

func TestAdminAuditLogsMutatingRequestsWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := gin.New()
	r.Use(RequestID())
	g := r.Group("/admin", AdminAudit(), AdminAuth("s3cret", ratelimit.New(ratelimit.PerMinute(60, 5))))
	g.GET("/x", ok)
	g.POST("/x", ok)

	do(r, "GET", "/admin/x", "1.1.1.1", map[string]string{"Authorization": "Bearer s3cret"}, "")
	if buf.Len() != 0 {
		t.Fatalf("read request audited: %s", buf.String())
	}
	do(r, "POST", "/admin/x", "1.1.1.1", map[string]string{"Authorization": "Bearer s3cret", "X-Request-ID": "rid-1"}, `{}`)
	do(r, "POST", "/admin/x", "1.1.1.1", map[string]string{"Authorization": "Bearer wrong-token"}, `{}`)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 audit lines, got %d: %s", len(lines), buf.String())
	}
	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["msg"] != "admin audit" || first["method"] != "POST" || first["path"] != "/admin/x" ||
		first["client_ip"] != "1.1.1.1" || first["status"] != float64(200) || first["request_id"] != "rid-1" {
		t.Fatalf("audit line: %v", first)
	}
	if strings.Contains(buf.String(), "s3cret") || strings.Contains(buf.String(), "wrong-token") {
		t.Fatal("token leaked into audit log")
	}
}
