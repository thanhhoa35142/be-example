package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type DigestHandler struct {
	svc *digest.Service
}

func NewDigestHandler(svc *digest.Service) *DigestHandler { return &DigestHandler{svc: svc} }

type digestRequest struct {
	SpotIDs []string `json:"spotIds"`
	Enabled *bool    `json:"enabled"`
}

func digestDTO(d *digest.Digest) gin.H {
	ids := d.SpotIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return gin.H{"spotIds": ids, "enabled": d.Enabled, "sendDow": d.SendDow, "sendHour": d.SendHour}
}

func deviceParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return uuid.Nil, false
	}
	return id, true
}

// PUT /v1/devices/:id/digest — create/replace the weekend digest (Plus).
func (h *DigestHandler) Put(c *gin.Context) {
	deviceID, ok := deviceParam(c)
	if !ok {
		return
	}
	var req digestRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.SpotIDs == nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected {spotIds:[uuid], enabled:bool}")
		return
	}
	ids := make([]uuid.UUID, 0, len(req.SpotIDs))
	for _, s := range req.SpotIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "spotIds must be UUIDs")
			return
		}
		ids = append(ids, id)
	}
	d, err := h.svc.Upsert(c.Request.Context(), deviceID, ids, *req.Enabled)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": digestDTO(d)})
}

// GET /v1/devices/:id/digest
func (h *DigestHandler) Get(c *gin.Context) {
	deviceID, ok := deviceParam(c)
	if !ok {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), deviceID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": digestDTO(d)})
}

// DELETE /v1/devices/:id/digest
func (h *DigestHandler) Delete(c *gin.Context) {
	deviceID, ok := deviceParam(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), deviceID); err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DigestHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, digest.ErrNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Digest not found", "")
	case errors.Is(err, digest.ErrDeviceInvalid):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	case errors.Is(err, digest.ErrInvalid):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid digest", err.Error())
	case errors.Is(err, digest.ErrPlanLimit):
		httpx.WriteProblem(c, http.StatusForbidden, httpx.CodePlanLimit, "Plan limit reached", err.Error())
	default:
		httpx.WriteError(c, err)
	}
}
