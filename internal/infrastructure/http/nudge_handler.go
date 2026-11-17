package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/nudge"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// NudgeHandler serves /v1/devices/:id/nudge (good-day push opt-in). Free and
// Plus alike: no plan gate.
type NudgeHandler struct {
	svc *nudge.Service
}

func NewNudgeHandler(svc *nudge.Service) *NudgeHandler { return &NudgeHandler{svc: svc} }

type nudgeRequest struct {
	Enabled  *bool   `json:"enabled"`
	Consent  *string `json:"consent"`
	SendHour *int    `json:"sendHour"`
	Home     *struct {
		Lat    *float64 `json:"lat"`
		Lng    *float64 `json:"lng"`
		Source *string  `json:"source"`
	} `json:"home"`
}

// nudgeDTO never carries the home cell or coordinate.
func nudgeDTO(s *nudge.Subscription) gin.H {
	var updated any // JSON null while no home is set
	if s.HomeUpdatedAt != nil {
		updated = s.HomeUpdatedAt.UTC().Format(time.RFC3339)
	}
	return gin.H{
		"enabled":       s.Enabled,
		"sendHour":      s.SendHour,
		"homeSet":       s.HomeSet(),
		"homeUpdatedAt": updated,
	}
}

const nudgeBodyHint = `expected {enabled?:bool, consent?:"explicit"|"provisional", sendHour?:7-10, home?:{lat,lng,source:"location"|"city"}}`

func (h *NudgeHandler) Put(c *gin.Context) {
	deviceID, ok := deviceParam(c)
	if !ok {
		return
	}
	var req nudgeRequest
	// The body holds a coordinate: it is parsed here and never logged or
	// echoed back, not even in error details.
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", nudgeBodyHint)
		return
	}
	in := nudge.PutInput{Enabled: req.Enabled, Consent: req.Consent, SendHour: req.SendHour}
	if req.Home != nil {
		if req.Home.Lat == nil || req.Home.Lng == nil || req.Home.Source == nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "home needs lat, lng and source")
			return
		}
		in.Home = &nudge.Home{Lat: *req.Home.Lat, Lng: *req.Home.Lng, Source: *req.Home.Source}
	}
	s, err := h.svc.Put(c.Request.Context(), deviceID, in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": nudgeDTO(s)})
}

func (h *NudgeHandler) Get(c *gin.Context) {
	deviceID, ok := deviceParam(c)
	if !ok {
		return
	}
	s, err := h.svc.Get(c.Request.Context(), deviceID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": nudgeDTO(s)})
}

func (h *NudgeHandler) Delete(c *gin.Context) {
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

func (h *NudgeHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, nudge.ErrNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Nudge subscription not found", "")
	case errors.Is(err, nudge.ErrDeviceInvalid):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	case errors.Is(err, nudge.ErrInvalid):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid nudge subscription", err.Error())
	default:
		httpx.WriteError(c, err)
	}
}
