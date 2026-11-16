package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

// Two HttpOnly cookies carry the admin session: a short-lived JWT access
// token for API calls and a long-lived single-use refresh token. MFA only
// gates login; the refresh token keeps the browser signed in across
// access-token expiry and browser restarts.
const (
	AdminAccessCookie  = "ph_admin_access"
	AdminRefreshCookie = "ph_admin_refresh"
)

const AdminCSRFHeader = "X-Pickhour-Admin"

type AdminAuthHandler struct {
	svc    *adminauth.Service
	failed ratelimit.Checker
}

func NewAdminAuthHandler(svc *adminauth.Service, failed ratelimit.Checker) *AdminAuthHandler {
	return &AdminAuthHandler{svc: svc, failed: failed}
}

func (h *AdminAuthHandler) Service() SessionAuthenticator { return h.svc }

func clientMeta(c *gin.Context) (userAgent, ip string) {
	return c.Request.UserAgent(), c.ClientIP()
}

func (h *AdminAuthHandler) denyFailed(c *gin.Context) bool {
	if denied, retry := h.failed.Deny(c.ClientIP()); denied {
		tooManyRequests(c, retry, "too many failed authentication attempts")
		return true
	}
	return false
}

func (h *AdminAuthHandler) hitFailed(c *gin.Context) { h.failed.Hit(c.ClientIP()) }

// adminCookieSecure controls the Secure flag on the admin cookies.
// Browsers drop Secure cookies sent over plain HTTP, which silently kills
// sessions — set ADMIN_COOKIE_SECURE=false only for HTTP dev/staging.
var adminCookieSecure = true

// SetAdminCookieSecure configures the session cookie Secure flag at boot.
func SetAdminCookieSecure(secure bool) { adminCookieSecure = secure }

func adminCookie(name, value string, ttl time.Duration) *http.Cookie {
	// Lax, not Strict: the Google OAuth callback is a cross-site top-level GET
	// and Strict would withhold the cookie, breaking account linking. Lax
	// still blocks cookie-bearing cross-site POSTs, and mutations additionally
	// require the X-Pickhour-Admin header + Origin check.
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   adminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl / time.Second),
	}
}

func (h *AdminAuthHandler) setAuthCookies(c *gin.Context, pair *adminauth.TokenPair) {
	http.SetCookie(c.Writer, adminCookie(AdminAccessCookie, pair.Access, h.svc.AccessTTL()))
	http.SetCookie(c.Writer, adminCookie(AdminRefreshCookie, pair.Refresh, h.svc.RefreshTTL()))
}

// clearAccessCookie drops only the access token. The middleware uses this on
// auth failure so an expired access token never destroys the refresh cookie —
// the client refreshes instead of re-authenticating.
func clearAccessCookie(c *gin.Context) {
	http.SetCookie(c.Writer, adminCookie(AdminAccessCookie, "", -time.Second))
}

// clearAuthCookies drops both credentials (logout, dead refresh token).
func clearAuthCookies(c *gin.Context) {
	http.SetCookie(c.Writer, adminCookie(AdminAccessCookie, "", -time.Second))
	http.SetCookie(c.Writer, adminCookie(AdminRefreshCookie, "", -time.Second))
}

func accessToken(c *gin.Context) string {
	v, err := c.Cookie(AdminAccessCookie)
	if err != nil {
		return ""
	}
	return v
}

func refreshToken(c *gin.Context) string {
	v, err := c.Cookie(AdminRefreshCookie)
	if err != nil {
		return ""
	}
	return v
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case adminauth.IsLocked(err):
		retry := adminauth.DefaultConfig().Lockout
		var le *adminauth.LockedError
		if errors.As(err, &le) && le.RetryAfter > 0 {
			retry = le.RetryAfter
		}
		secs := int(retry.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		c.Header("Retry-After", strconv.Itoa(secs))
		httpx.WriteProblem(c, http.StatusTooManyRequests, httpx.CodeRateLimited,
			"Too many attempts", "account temporarily locked")
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Unauthorized", "invalid email or password")
	case errors.Is(err, adminauth.ErrInvalidCode):
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Unauthorized", "invalid or expired code")
	case errors.Is(err, adminauth.ErrInvalidChallenge):
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Unauthorized", "sign-in challenge expired, start again")
	case errors.Is(err, adminauth.ErrInvalidSession):
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Unauthorized", "session expired")
	default:
		httpx.WriteError(c, err)
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AdminAuthHandler) Login(c *gin.Context) {
	if h.denyFailed(c) {
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected email + password")
		return
	}
	ua, ip := clientMeta(c)
	res, err := h.svc.Login(c.Request.Context(), req.Email, req.Password, ua, ip)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			h.hitFailed(c)
		}
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

type codeRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

func (h *AdminAuthHandler) TOTP(c *gin.Context) {
	if h.denyFailed(c) {
		return
	}
	var req codeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected challenge + code")
		return
	}
	ua, ip := clientMeta(c)
	res, pair, err := h.svc.ConfirmCode(c.Request.Context(), req.Challenge, req.Code, ua, ip)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidCode) || errors.Is(err, adminauth.ErrInvalidCredentials) {
			h.hitFailed(c)
		}
		writeAuthError(c, err)
		return
	}
	h.setAuthCookies(c, pair)
	c.Set("adminUserID", res.User.ID)
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *AdminAuthHandler) Enroll(c *gin.Context) {
	if h.denyFailed(c) {
		return
	}
	var req codeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected challenge + code")
		return
	}
	ua, ip := clientMeta(c)
	res, pair, err := h.svc.ConfirmEnroll(c.Request.Context(), req.Challenge, req.Code, ua, ip)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidCode) {
			h.hitFailed(c)
		}
		writeAuthError(c, err)
		return
	}
	h.setAuthCookies(c, pair)
	c.Set("adminUserID", res.User.ID)
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// Refresh rotates the refresh-token cookie into a new access+refresh pair.
// It is public (no access auth) and single-use: replayed or concurrent
// refreshes fail and the client must log in again.
func (h *AdminAuthHandler) Refresh(c *gin.Context) {
	if h.denyFailed(c) {
		return
	}
	ua, ip := clientMeta(c)
	pair, user, err := h.svc.RefreshSession(c.Request.Context(), refreshToken(c), ua, ip)
	if err != nil {
		clearAuthCookies(c)
		writeAuthError(c, err)
		return
	}
	h.setAuthCookies(c, pair)
	c.Set("adminUserID", user.ID.String())
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"user": adminauth.UserInfo{ID: user.ID.String(), Email: user.Email}}})
}

func (h *AdminAuthHandler) Me(c *gin.Context) {
	_, user, err := h.svc.Me(c.Request.Context(), accessToken(c))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"user": adminauth.UserInfo{ID: user.ID.String(), Email: user.Email}}})
}

// Logout revokes the refresh session. It is public so an expired access
// token can still end the session; the access JWT dies on its own TTL.
func (h *AdminAuthHandler) Logout(c *gin.Context) {
	_ = h.svc.Logout(c.Request.Context(), refreshToken(c))
	clearAuthCookies(c)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "ok"}})
}

func (h *AdminAuthHandler) RecoveryCodes(c *gin.Context) {
	codes, err := h.svc.RegenerateRecoveryCodes(c.Request.Context(), accessToken(c))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"recoveryCodes": codes}})
}

func (h *AdminAuthHandler) Sessions(c *gin.Context) {
	items, err := h.svc.Sessions(c.Request.Context(), refreshToken(c))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": items}})
}

func (h *AdminAuthHandler) RevokeOtherSessions(c *gin.Context) {
	if err := h.svc.RevokeOtherSessions(c.Request.Context(), refreshToken(c)); err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "ok"}})
}

type passwordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *AdminAuthHandler) Password(c *gin.Context) {
	if h.denyFailed(c) {
		return
	}
	var req passwordRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected currentPassword + newPassword")
		return
	}
	err := h.svc.ChangePassword(c.Request.Context(), accessToken(c), refreshToken(c), req.CurrentPassword, req.NewPassword)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "ok"}})
	case errors.Is(err, adminauth.ErrWrongPassword):
		h.hitFailed(c)
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeWrongPassword, "Wrong password", "current password is incorrect")
	case errors.Is(err, adminauth.ErrWeakPassword):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeWeakPassword, "Weak password",
			"new password must be at least "+strconv.Itoa(adminauth.MinPasswordLength)+" characters")
	case errors.Is(err, adminauth.ErrPasswordUnchanged):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodePasswordUnchanged, "Password unchanged",
			"new password must differ from the current one")
	default:
		writeAuthError(c, err)
	}
}
