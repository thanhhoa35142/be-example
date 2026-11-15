package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/events"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type EventsHandler struct {
	svc *events.Service
}

func NewEventsHandler(svc *events.Service) *EventsHandler {
	return &EventsHandler{svc: svc}
}

type trackEventsRequest struct {
	Events []events.Input `json:"events"`
}

func (h *EventsHandler) Track(c *gin.Context) {
	var req trackEventsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected {events[]}")
		return
	}
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	if err := h.svc.TrackBatch(c.Request.Context(), deviceID, req.Events); err != nil {
		switch {
		case errors.Is(err, events.ErrInvalidEvent):
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid events", err.Error())
		case errors.Is(err, events.ErrDeviceInvalid):
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
		case errors.Is(err, events.ErrRateLimited):
			httpx.WriteProblem(c, http.StatusTooManyRequests, httpx.CodeRateLimited, "Rate limited", "too many events for this device")
		default:
			httpx.WriteError(c, err)
		}
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"status": "accepted", "count": len(req.Events)}})
}
