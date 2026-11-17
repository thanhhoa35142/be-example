package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
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

// TokenRateLimit is an extra per-IP limiter for routes keyed by a share
// token, on top of the global Read/Write rules.
func TokenRateLimit(lim ratelimit.Checker) gin.HandlerFunc {
	return func(c *gin.Context) {
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

// AuditRecorder persists an admin mutation record. Implemented by the admin
// service; nil disables DB persistence (tests).
type AuditRecorder interface {
	RecordAudit(ctx context.Context, e admin.AuditEvent) error
}

func AdminAudit(rec AuditRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if isSafeMethod(c.Request.Method) {
			return
		}
		method := c.GetString("adminAuthMethod")
		entry := admin.AuditEvent{
			AuthMethod: method,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			Status:     c.Writer.Status(),
			ClientIP:   c.ClientIP(),
		}
		if uid := c.GetString("adminUserID"); uid != "" {
			entry.AdminUserID = &uid
		}
		if rid := c.GetString("requestID"); rid != "" {
			entry.RequestID = &rid
		}
		slog.InfoContext(c.Request.Context(), "admin audit",
			"request_id", c.GetString("requestID"),
			"method", entry.Method,
			"path", entry.Path,
			"client_ip", entry.ClientIP,
			"admin_user_id", c.GetString("adminUserID"),
			"status", entry.Status,
		)
		if rec != nil {
			if err := rec.RecordAudit(c.Request.Context(), entry); err != nil {
				slog.ErrorContext(c.Request.Context(), "admin audit write failed",
					"request_id", entry.RequestID, "error", err)
			}
		}
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
		cookie, err := c.Cookie(AdminAccessCookie)
		if err != nil {
			cookie = ""
		}
		sess, user, err := sessions.Authenticate(c.Request.Context(), cookie)
		if err != nil {

			clearAccessCookie(c)
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

func originAllowed(c *gin.Context, origin string, allowedOrigins, trustedProxies []string) bool {
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

		if au, aerr := url.Parse(allowed); aerr == nil && au.Host != "" {
			if !strings.EqualFold(u.Host, au.Host) {
				continue
			}
			// When the allowlist entry carries a scheme, the Origin scheme
			// must match too — otherwise http:// would pass an https:// rule.
			if au.Scheme != "" && !strings.EqualFold(u.Scheme, au.Scheme) {
				continue
			}
			return true
		}
	}
	// X-Forwarded-Host is only meaningful when the request actually arrived
	// through a configured trusted proxy (e.g. the Next.js rewrite); honoring
	// it from arbitrary peers would let a caller spoof the Origin allowlist.
	// The forwarded value must itself be an allowlisted host — echoing an
	// arbitrary XFH would defeat the check entirely.
	if xfh := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); xfh != "" && remoteAddrTrusted(c, trustedProxies) {
		if !originHostAllowed(xfh, allowedOrigins) {
			return false
		}
		return strings.EqualFold(u.Host, xfh)
	}
	return strings.EqualFold(u.Host, c.Request.Host)
}

// originHostAllowed reports whether hostport (host or host:port, no scheme)
// matches the host of any configured allowed origin.
func originHostAllowed(hostport string, allowedOrigins []string) bool {
	hostport = strings.TrimSpace(hostport)
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.EqualFold(hostport, allowed) {
			return true
		}
		if au, err := url.Parse(allowed); err == nil && au.Host != "" && strings.EqualFold(hostport, au.Host) {
			return true
		}
	}
	return false
}

// remoteAddrTrusted reports whether the TCP peer is one of the configured
// trusted proxies (IP or CIDR), mirroring what gin uses for ClientIP.
func remoteAddrTrusted(c *gin.Context, proxies []string) bool {
	if len(proxies) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			if _, n, err := net.ParseCIDR(p); err == nil && n.Contains(ip) {
				return true
			}
			continue
		}
		if pip := net.ParseIP(p); pip != nil && pip.Equal(ip) {
			return true
		}
	}
	return false
}

func AdminCSRF(allowedOrigins, trustedProxies []string) gin.HandlerFunc {
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
		if origin := c.GetHeader("Origin"); origin != "" && !originAllowed(c, origin, allowedOrigins, trustedProxies) {
			httpx.WriteProblem(c, http.StatusForbidden, httpx.CodeForbidden,
				"Forbidden", "origin not allowed")
			c.Abort()
			return
		}
		c.Next()
	}
}
