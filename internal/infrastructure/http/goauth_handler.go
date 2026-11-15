package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/goauth"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// GoAuthHandler exposes the Google account link for the admin console.
// Every endpoint requires a session login — the CLI bearer token has no
// admin user to attach the grant to.
type GoAuthHandler struct {
	svc *goauth.Service
}

func NewGoAuthHandler(svc *goauth.Service) *GoAuthHandler {
	return &GoAuthHandler{svc: svc}
}

// adminUserID returns the session's admin user, or writes 401 and false.
func (h *GoAuthHandler) adminUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetString("adminUserID")
	if raw == "" {
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Session login required", "link a Google account from the console")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Session login required", "")
		return uuid.Nil, false
	}
	return id, true
}

// Connect returns the Google consent URL the browser should navigate to.
func (h *GoAuthHandler) Connect(c *gin.Context) {
	id, ok := h.adminUserID(c)
	if !ok {
		return
	}
	url, err := h.svc.AuthURL(id)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"url": url}})
}

// Callback receives the redirect from Google. On success it bounces the
// browser back to the console's /security page.
func (h *GoAuthHandler) Callback(c *gin.Context) {
	id, ok := h.adminUserID(c)
	if !ok {
		return
	}
	code := c.Query("code")
	state := c.Query("state")
	errParam := c.Query("error")
	if errParam != "" || code == "" {
		c.Redirect(http.StatusFound, "/security?google=error")
		return
	}
	if err := h.svc.HandleCallback(c.Request.Context(), id, code, state); err != nil {
		h.writeError(c, err)
		return
	}
	c.Redirect(http.StatusFound, "/security?google=connected")
}

func (h *GoAuthHandler) Status(c *gin.Context) {
	id, ok := h.adminUserID(c)
	if !ok {
		return
	}
	st, err := h.svc.Status(c.Request.Context(), id)
	if errors.Is(err, goauth.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"linked": false}})
		return
	}
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"linked":    true,
		"email":     st.Email,
		"scopes":    st.Scopes,
		"createdAt": st.CreatedAt,
		"updatedAt": st.UpdatedAt,
	}})
}

func (h *GoAuthHandler) Disconnect(c *gin.Context) {
	id, ok := h.adminUserID(c)
	if !ok {
		return
	}
	if err := h.svc.Disconnect(c.Request.Context(), id); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "disconnected"}})
}

func (h *GoAuthHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, goauth.ErrNotConfigured):
		httpx.WriteProblem(c, http.StatusServiceUnavailable, "not_configured",
			"Google sign-in is not configured", "")
	case errors.Is(err, goauth.ErrStateInvalid), errors.Is(err, goauth.ErrStateExpired):
		httpx.WriteProblem(c, http.StatusBadRequest, "invalid_state",
			"The sign-in link expired — try connecting again", "")
	case errors.Is(err, goauth.ErrNoRefreshToken):
		httpx.WriteProblem(c, http.StatusBadGateway, "no_refresh_token",
			"Google did not return a refresh token", "revoke pickhour's access at myaccount.google.com and try again")
	case errors.Is(err, goauth.ErrExchangeFailed), errors.Is(err, goauth.ErrUserInfoFailed):
		httpx.WriteProblem(c, http.StatusBadGateway, "google_error",
			"Google request failed", "")
	default:
		httpx.WriteError(c, err)
	}
}
