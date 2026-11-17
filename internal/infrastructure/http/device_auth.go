package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

const (
	// DeviceIDHeader carries the device UUID on routes that have no :id path
	// parameter (alerts, reports, events, entitlement link). It replaces the
	// legacy ?deviceId= query parameter, which leaked the credential into
	// access logs and browser history.
	DeviceIDHeader = "X-Device-ID"
	// DeviceTokenHeader carries the per-device secret issued at registration.
	// It is deliberately NOT Authorization: a junk Authorization header must
	// not interact with the public response cache.
	DeviceTokenHeader = "X-Device-Token"
)

// deviceAuthParam authenticates the device identified by the :id path
// parameter (devices/:id/... routes).
func deviceAuthParam(svc *devices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
			return
		}
		deviceAuthCheck(c, svc, id)
	}
}

// deviceAuthHeader authenticates the device identified by X-Device-ID.
// Used on routes whose path id is not a device (e.g. /alerts/:id is a rule).
func deviceAuthHeader(svc *devices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.GetHeader(DeviceIDHeader))
		if err != nil {
			httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
				"Device authentication required", "send the device UUID in "+DeviceIDHeader)
			return
		}
		deviceAuthCheck(c, svc, id)
	}
}

func deviceAuthCheck(c *gin.Context, svc *devices.Service, id uuid.UUID) {
	ok, err := svc.Authenticate(c.Request.Context(), id, c.GetHeader(DeviceTokenHeader))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	if !ok {
		httpx.WriteProblem(c, http.StatusUnauthorized, httpx.CodeUnauthorized,
			"Device authentication required", "send the device secret in "+DeviceTokenHeader)
		return
	}
	// Activity signal for the good-day push (last_seen_at). Best effort: it
	// must never fail or slow the request.
	if err := svc.Touch(c.Request.Context(), id); err != nil {
		slog.WarnContext(c.Request.Context(), "device touch failed", "device", id, "error", err)
	}
	c.Next()
}
