package http

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

// RateLimits are the per-client-IP budgets by request class.
type RateLimits struct {
	Read   ratelimit.Rule // GET/HEAD/OPTIONS
	Write  ratelimit.Rule // POST/PUT/PATCH/DELETE
	Device ratelimit.Rule // POST /v1/devices (free identity minting)
}

// DefaultRateLimits are used when none are configured.
func DefaultRateLimits() RateLimits {
	return RateLimits{
		Read:   ratelimit.PerMinute(600, 120),
		Write:  ratelimit.PerMinute(60, 30),
		Device: ratelimit.PerMinute(30.0/60, 10), // 30 per hour
	}
}

func isHealthPath(p string) bool { return p == "/healthz" || p == "/readyz" }

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func tooManyRequests(c *gin.Context, retryAfter time.Duration, detail string) {
	secs := int(retryAfter.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	httpx.WriteProblem(c, http.StatusTooManyRequests, httpx.CodeRateLimited, "Rate limited", detail)
	c.Abort()
}

// RateLimit applies per-client-IP token buckets: reads get the widest budget,
// writes a tighter one, and POST /v1/devices the tightest (device ids are free
// to mint). /healthz and /readyz are exempt. The client IP is c.ClientIP(),
// which only honours X-Forwarded-For from the engine's trusted proxies.
// mk builds the checker per rule class; nil = in-memory limiters.
func RateLimit(l RateLimits, mk ...func(ratelimit.Rule, string) ratelimit.Checker) gin.HandlerFunc {
	newLim := func(r ratelimit.Rule, ns string) ratelimit.Checker {
		if len(mk) > 0 && mk[0] != nil {
			return mk[0](r, ns)
		}
		return ratelimit.New(r)
	}
	read, write, device := newLim(l.Read, "read"), newLim(l.Write, "write"), newLim(l.Device, "device")
	return func(c *gin.Context) {
		if isHealthPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		lim := write
		switch {
		case isSafeMethod(c.Request.Method):
			lim = read
		case c.Request.Method == http.MethodPost && c.Request.URL.Path == "/v1/devices":
			lim = device
		}
		if ok, retry := lim.Allow(c.ClientIP()); !ok {
			tooManyRequests(c, retry, "too many requests, slow down")
			return
		}
		c.Next()
	}
}

// BodyLimit caps request bodies with http.MaxBytesReader. overrides maps a
// route pattern (gin FullPath, e.g. "/v1/events") to a larger/smaller cap.
// The body is read eagerly (at most limit+1 bytes) so an oversize request
// gets one uniform 413 problem response instead of every handler having to
// translate a bind error.
func BodyLimit(def int64, overrides map[string]int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Body == http.NoBody {
			c.Next()
			return
		}
		limit := def
		if v, ok := overrides[c.FullPath()]; ok {
			limit = v
		}
		tooLarge := func() {
			httpx.WriteProblem(c, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge,
				"Payload too large", "request body exceeds "+strconv.FormatInt(limit, 10)+" bytes")
			c.Abort()
		}
		if c.Request.ContentLength > limit {
			tooLarge()
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooLarge()
				return
			}
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "could not read request body")
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

// AdminAudit writes one structured log line for every mutating admin request
// (including rejected ones). Never logs the Authorization header or body.
func AdminAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if isSafeMethod(c.Request.Method) {
			return
		}
		slog.InfoContext(c.Request.Context(), "admin audit",
			"request_id", c.GetString("requestID"),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"client_ip", c.ClientIP(),
			"status", c.Writer.Status(),
		)
	}
}

// AdminAuth guards staff endpoints with a bearer token. An empty token
// disables the admin surface entirely (404). The comparison is constant time
// and failed attempts are rate-limited per client IP (successful requests do
// not consume the budget).
func AdminAuth(token string, failed ratelimit.Checker) gin.HandlerFunc {
	want := sha256.Sum256([]byte(token))
	return func(c *gin.Context) {
		if token == "" {
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Not found", "")
			c.Abort()
			return
		}
		ip := c.ClientIP()
		if denied, retry := failed.Deny(ip); denied {
			tooManyRequests(c, retry, "too many failed authentication attempts")
			return
		}
		got, _ := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got)) // fixed-length digests: no length leak
		if got == "" || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			failed.Hit(ip)
			c.Header("WWW-Authenticate", `Bearer realm="pickhour-admin"`)
			httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "Unauthorized", "")
			c.Abort()
			return
		}
		c.Next()
	}
}
