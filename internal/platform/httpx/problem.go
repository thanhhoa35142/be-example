// Package httpx provides shared HTTP helpers: RFC 7807 problem details.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Stable machine-readable error codes. Clients switch on Code, never Title.
const (
	CodeBadRequest    = "bad_request"
	CodeInvalidID     = "invalid_id"
	CodeInvalidBBox   = "invalid_bbox"
	CodeBboxTooLarge  = "bbox_too_large"
	CodeInvalidWhen   = "invalid_when"
	CodeInvalidQuery  = "invalid_query"
	CodeInvalidBody   = "invalid_body"
	CodeInvalidReport = "invalid_report"
	CodeNotFound      = "not_found"
	CodeMergedSpot    = "merged_spot"
	CodeConflict      = "conflict"
	CodeRateLimited   = "rate_limited"
	CodePlanLimit     = "plan_limit"
	CodeInternal      = "internal_error"
	// CodePayloadTooLarge: request body over the size limit (413).
	CodePayloadTooLarge = "payload_too_large"
	// CodeUnauthorized: missing/invalid staff credentials (401).
	CodeUnauthorized = "unauthorized"
	// CodeDeviceLimit: subscription already linked to the max number of
	// active devices (409).
	CodeDeviceLimit = "device_limit"
	// CodeAccountMismatch: the transaction's appAccountToken belongs to a
	// different device (403).
	CodeAccountMismatch = "account_mismatch"
	// CodeUnavailable: a required server-side capability is not configured
	// (503), e.g. App Store verification.
	CodeUnavailable = "service_unavailable"
)

// Problem is an RFC 7807 problem-details body extended with a stable
// machine-readable Code and the RequestID for support correlation.
type Problem struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// WriteProblem responds with a problem+json body.
func WriteProblem(c *gin.Context, status int, code, title, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, Problem{
		Type:      "about:blank",
		Code:      code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		RequestID: c.GetString("requestID"),
	})
}

// WriteError logs the underlying error server-side (with request id for
// correlation) and returns a generic 500 — internals are never leaked.
func WriteError(c *gin.Context, err error) {
	slog.ErrorContext(c.Request.Context(), "request failed",
		"request_id", c.GetString("requestID"),
		"client", c.GetString("client"),
		"client_version", c.GetString("clientVersion"),
		"user_agent", c.GetString("userAgent"),
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"error", err,
	)
	WriteProblem(c, http.StatusInternalServerError, CodeInternal, "Internal Server Error", "")
}
