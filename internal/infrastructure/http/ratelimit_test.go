package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

func rateLimitedRouter(l RateLimits) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(l))
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/v1/x", ok)
	r.POST("/v1/devices", ok)
	return r
}

func hit(r *gin.Engine, method, path, ip, device string) int {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = ip + ":1234"
	if device != "" {
		req.Header.Set(DeviceIDHeader, device)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// Installs behind one IP (carrier NAT) each get their own budget.
func TestRateLimitPerInstallBehindSharedIP(t *testing.T) {
	r := rateLimitedRouter(RateLimits{
		Read:   ratelimit.PerMinute(0.001, 2),
		Write:  ratelimit.PerMinute(0.001, 2),
		Device: ratelimit.PerMinute(0.001, 2),
	})
	a, b := uuid.NewString(), uuid.NewString()
	for i := range 2 {
		if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.1", a); code != http.StatusOK {
			t.Fatalf("install a request %d: got %d", i, code)
		}
	}
	if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.1", a); code != http.StatusTooManyRequests {
		t.Fatalf("install a over its budget: got %d, want 429", code)
	}
	if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.1", b); code != http.StatusOK {
		t.Fatalf("install b on the same IP: got %d, want 200", code)
	}
}

// Rotating X-Device-ID still hits the per-IP ceiling.
func TestRateLimitIPCeiling(t *testing.T) {
	r := rateLimitedRouter(RateLimits{
		Read:     ratelimit.PerMinute(0.001, 5),
		Write:    ratelimit.PerMinute(0.001, 5),
		Device:   ratelimit.PerMinute(0.001, 5),
		IPDevice: ratelimit.PerMinute(0.001, 3),
	})
	for i := range 3 {
		if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.2", uuid.NewString()); code != http.StatusOK {
			t.Fatalf("request %d under the ceiling: got %d", i, code)
		}
	}
	if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.2", uuid.NewString()); code != http.StatusTooManyRequests {
		t.Fatalf("fourth fresh id from one IP: got %d, want 429", code)
	}
	if code := hit(r, http.MethodPost, "/v1/devices", "10.0.0.3", uuid.NewString()); code != http.StatusOK {
		t.Fatalf("another IP: got %d, want 200", code)
	}
}

// No (or an invalid) X-Device-ID: counted per IP, as before.
func TestRateLimitWithoutDeviceCountsPerIP(t *testing.T) {
	r := rateLimitedRouter(RateLimits{
		Read:   ratelimit.PerMinute(0.001, 2),
		Write:  ratelimit.PerMinute(0.001, 2),
		Device: ratelimit.PerMinute(0.001, 2),
	})
	for _, device := range []string{"", "not-a-uuid"} {
		if code := hit(r, http.MethodGet, "/v1/x", "10.0.0.4", device); code != http.StatusOK {
			t.Fatalf("device %q: got %d", device, code)
		}
	}
	if code := hit(r, http.MethodGet, "/v1/x", "10.0.0.4", ""); code != http.StatusTooManyRequests {
		t.Fatalf("third anonymous read from one IP: got %d, want 429", code)
	}
}
