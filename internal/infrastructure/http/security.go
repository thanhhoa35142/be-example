package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

type RateLimits struct {
	Read   ratelimit.Rule
	Write  ratelimit.Rule
	Device ratelimit.Rule
}

func DefaultRateLimits() RateLimits {
	return RateLimits{
		Read:   ratelimit.PerMinute(600, 120),
		Write:  ratelimit.PerMinute(60, 30),
		Device: ratelimit.PerMinute(30.0/60, 10),
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
			"admin_user_id", c.GetString("adminUserID"),
			"status", c.Writer.Status(),
		)
	}
}

type SessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*adminauth.Session, *adminauth.User, error)
}

func AdminAuth(token string, sessions SessionAuthenticator, failed ratelimit.Checker) gin.HandlerFunc {
	want := sha256.Sum256([]byte(token))
	return func(c *gin.Context) {
		if token == "" && sessions == nil {
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Not found", "")
			c.Abort()
			return
		}
		ip := c.ClientIP()
		if denied, retry := failed.Deny(ip); denied {
			tooManyRequests(c, retry, "too many failed authentication attempts")
			return
		}
		if h := c.GetHeader("Authorization"); h != "" {
			got, found := strings.CutPrefix(h, "Bearer ")
			sum := sha256.Sum256([]byte(got))
			if !found || token == "" || got == "" || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
				failed.Hit(ip)
				c.Header("WWW-Authenticate", `Bearer realm="pickhour-admin"`)
				httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "Unauthorized", "")
				c.Abort()
				return
			}
			c.Set("adminAuthMethod", "token")
			c.Next()
			return
		}
		if sessions == nil {
			c.Header("WWW-Authenticate", `Bearer realm="pickhour-admin"`)
			httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "Unauthorized", "")
			c.Abort()
			return
		}
		cookie, err := c.Cookie(AdminSessionCookie)
		if err != nil {
			cookie = ""
		}
		sess, user, err := sessions.Authenticate(c.Request.Context(), cookie)
		if err != nil {

			clearSessionCookie(c)
			httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "Unauthorized", "")
			c.Abort()
			return
		}
		c.Set("adminAuthMethod", "session")
		c.Set("adminUserID", user.ID.String())
		c.Set("adminSessionID", sess.ID.String())
		c.Next()
	}
}

func originAllowed(c *gin.Context, origin string, allowedOrigins []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.EqualFold(u.Host, allowed) || strings.EqualFold(origin, allowed) {
			return true
		}

		if au, aerr := url.Parse(allowed); aerr == nil && au.Host != "" && strings.EqualFold(u.Host, au.Host) {
			return true
		}
	}
	if xfh := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); xfh != "" {
		return strings.EqualFold(u.Host, xfh)
	}
	return strings.EqualFold(u.Host, c.Request.Host)
}

func AdminCSRF(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) ||
			strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
			c.Next()
			return
		}
		if c.GetHeader(AdminCSRFHeader) != "1" {
			httpx.WriteProblem(c, http.StatusForbidden, httpx.CodeForbidden,
				"Forbidden", "missing "+AdminCSRFHeader+" header")
			c.Abort()
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" && !originAllowed(c, origin, allowedOrigins) {
			httpx.WriteProblem(c, http.StatusForbidden, httpx.CodeForbidden,
				"Forbidden", "origin not allowed")
			c.Abort()
			return
		}
		c.Next()
	}
}
