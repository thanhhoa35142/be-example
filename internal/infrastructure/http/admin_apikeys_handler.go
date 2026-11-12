package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// API-key management for the admin surface. The raw secret is returned
// exactly once, in the create response — it is never retrievable again.

type apiKeyCreateRequest struct {
	Name         string              `json:"name" binding:"required"`
	Description  string              `json:"description"`
	ExpiresAt    *time.Time          `json:"expiresAt"`
	RateLimitRPS int                 `json:"rateLimitRps"`
	Grants       []apikeys.GrantSpec `json:"grants" binding:"required,min=1"`
}

type apiKeyGrantRequest struct {
	Effect   string `json:"effect" binding:"required"`
	Resource string `json:"resource" binding:"required"`
	Action   string `json:"action" binding:"required"`
}

func writeKeyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, apikeys.ErrInvalidKey),
		errors.Is(err, apikeys.ErrInvalidGrant):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid request", err.Error())
	case errors.Is(err, apikeys.ErrKeyNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "API key not found", "")
	default:
		httpx.WriteError(c, err)
	}
}

func parseKeyID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid id", "expected a UUID")
		return uuid.Nil, false
	}
	return id, true
}

// GET /v1/admin/api-keys — all keys with their grants.
func (h *AdminHandler) APIKeys(c *gin.Context) {
	items, err := h.keys.ListWithGrants(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": items}})
}

// POST /v1/admin/api-keys — create a key; response includes the secret once.
func (h *AdminHandler) CreateAPIKey(c *gin.Context) {
	var req apiKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid request", err.Error())
		return
	}
	issued, err := h.keys.Create(c.Request.Context(), req.Name, req.Description,
		req.Grants, req.ExpiresAt, req.RateLimitRPS)
	if err != nil {
		writeKeyError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": issued})
}

// POST /v1/admin/api-keys/:id/revoke — immediately invalidate a key.
func (h *AdminHandler) RevokeAPIKey(c *gin.Context) {
	id, ok := parseKeyID(c)
	if !ok {
		return
	}
	if err := h.keys.Revoke(c.Request.Context(), id); err != nil {
		writeKeyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "revoked"}})
}

// POST /v1/admin/api-keys/:id/grants — add an allow/deny grant.
func (h *AdminHandler) AddAPIKeyGrant(c *gin.Context) {
	id, ok := parseKeyID(c)
	if !ok {
		return
	}
	var req apiKeyGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid request", err.Error())
		return
	}
	err := h.keys.AddGrant(c.Request.Context(), id, apikeys.Grant{
		Effect: req.Effect, Resource: req.Resource, Action: req.Action,
	})
	if err != nil {
		writeKeyError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"status": "added"}})
}

// DELETE /v1/admin/api-keys/:id/grants — remove a grant (body = grant triple).
func (h *AdminHandler) RemoveAPIKeyGrant(c *gin.Context) {
	id, ok := parseKeyID(c)
	if !ok {
		return
	}
	var req apiKeyGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid request", err.Error())
		return
	}
	err := h.keys.RemoveGrant(c.Request.Context(), id, apikeys.Grant{
		Effect: req.Effect, Resource: req.Resource, Action: req.Action,
	})
	if err != nil {
		writeKeyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "removed"}})
}
