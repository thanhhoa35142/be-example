package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type DevicesHandler struct {
	svc *devices.Service
}

func NewDevicesHandler(svc *devices.Service) *DevicesHandler {
	return &DevicesHandler{svc: svc}
}

type registerDeviceRequest struct {
	ID         string         `json:"id"`
	APNsToken  *string        `json:"apnsToken"`
	APNsEnv    *string        `json:"apnsEnv"`
	Locale     *string        `json:"locale"`
	TZ         *string        `json:"tz"`
	AppVersion *string        `json:"appVersion"`
	Prefs      map[string]any `json:"prefs"`
}

// POST /v1/devices — register or refresh a device (id is client-generated).
func (h *DevicesHandler) Register(c *gin.Context) {
	var req registerDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON device registration")
		return
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	outID, err := h.svc.Register(c.Request.Context(), devices.RegisterInput{
		ID: id, APNsToken: req.APNsToken, APNsEnv: req.APNsEnv,
		Locale: req.Locale, TZ: req.TZ, AppVersion: req.AppVersion, Prefs: req.Prefs,
	})
	if err != nil {
		if errors.Is(err, devices.ErrInvalidInput) {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid device", err.Error())
			return
		}
		if errors.Is(err, devices.ErrConflict) {
			httpx.WriteProblem(c, http.StatusConflict, httpx.CodeConflict, "Conflict", "apnsToken is being registered concurrently, retry")
			return
		}
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"id": outID.String()}})
}

// DELETE /v1/devices/:id — soft-delete (GDPR/CCPA unlink). Device ID acts as
// the capability token until a real auth scheme exists.
func (h *DevicesHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	deleted, err := h.svc.Delete(c.Request.Context(), id)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	if !deleted {
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
		return
	}
	c.Status(http.StatusNoContent)
}
