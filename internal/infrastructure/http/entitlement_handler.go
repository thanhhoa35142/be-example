package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type EntitlementHandler struct {
	svc *entitlement.Service
}

func NewEntitlementHandler(svc *entitlement.Service) *EntitlementHandler {
	return &EntitlementHandler{svc: svc}
}

// GET /v1/devices/:id/entitlement — resolved plan for a device.
func (h *EntitlementHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	e, err := h.svc.Entitlement(c.Request.Context(), id.String())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	out := gin.H{"plan": string(e.Plan)}
	if e.Subscription != nil {
		out["productId"] = e.Subscription.ProductID
		if e.Subscription.ExpiresAt != nil {
			out["expiresAt"] = e.Subscription.ExpiresAt.UTC()
		}
		out["isTrial"] = e.Subscription.IsTrial
		out["autoRenew"] = e.Subscription.AutoRenew
		out["ownership"] = e.Subscription.Ownership
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

type linkTransactionRequest struct {
	DeviceID          string `json:"deviceId"`
	SignedTransaction string `json:"signedTransaction"`
}

// POST /v1/entitlement/link — attach a StoreKit signed transaction to a device.
func (h *EntitlementHandler) Link(c *gin.Context) {
	var req linkTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON with deviceId and signedTransaction")
		return
	}
	if _, err := uuid.Parse(req.DeviceID); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}
	if req.SignedTransaction == "" {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Missing signedTransaction", "")
		return
	}
	e, err := h.svc.LinkTransaction(c.Request.Context(), req.DeviceID, req.SignedTransaction)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"plan": string(e.Plan)}})
}

type appStoreNotificationRequest struct {
	SignedPayload string `json:"signedPayload"`
}

// POST /v1/appstore/notifications — App Store Server Notifications V2 webhook.
// TODO(production): authenticate the caller (Apple does not sign the outer
// request; verify the JWS payload chain before trusting contents).
func (h *EntitlementHandler) AppStoreNotification(c *gin.Context) {
	var req appStoreNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON with signedPayload")
		return
	}
	if req.SignedPayload == "" {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Missing signedPayload", "")
		return
	}
	if err := h.svc.ProcessNotification(c.Request.Context(), req.SignedPayload); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "accepted"}})
}

func (h *EntitlementHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, entitlement.ErrDeviceNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	case errors.Is(err, entitlement.ErrInvalidJWS), errors.Is(err, entitlement.ErrInvalidNotice):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid signed payload", "")
	default:
		httpx.WriteError(c, err)
	}
}
