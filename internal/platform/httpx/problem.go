package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	CodeBadRequest    = "bad_request"
	CodeInvalidID     = "invalid_id"
	CodeInvalidBBox   = "invalid_bbox"
	CodeBboxTooLarge  = "bbox_too_large"
	CodeInvalidWhen   = "invalid_when"
	CodeInvalidStart  = "invalid_start"
	CodeInvalidRange  = "invalid_range"
	CodeInvalidQuery  = "invalid_query"
	CodeInvalidBody   = "invalid_body"
	CodeInvalidReport = "invalid_report"
	CodeNotFound      = "not_found"
	CodeMergedSpot    = "merged_spot"
	CodeConflict      = "conflict"
	CodeRateLimited   = "rate_limited"
	CodePlanLimit     = "plan_limit"
	CodeInternal      = "internal_error"

	// Plans (C2): a plan already has the maximum followers; the device
	// already follows the maximum number of plans; a device cannot follow
	// its own plan.
	CodeFollowerLimit = "follower_limit"
	CodeFollowLimit   = "follow_limit"
	CodeOwnPlan       = "own_plan"

	CodePayloadTooLarge = "payload_too_large"

	CodeUnauthorized = "unauthorized"

	CodeDeviceLimit = "device_limit"

	CodeAccountMismatch = "account_mismatch"

	CodeForbidden = "forbidden"

	CodeWrongPassword = "wrong_password"

	CodeWeakPassword = "weak_password"

	CodePasswordUnchanged = "password_unchanged"

	CodeUnavailable = "service_unavailable"

	CodeTimeout = "timeout"
)

type Problem struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

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

func isDBTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "57014" || pgErr.Code == "55P03"
	}
	return false
}

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
	if isDBTimeout(err) {
		c.Header("Retry-After", "1")
		WriteProblem(c, http.StatusServiceUnavailable, CodeTimeout, "Service Unavailable", "")
		return
	}
	WriteProblem(c, http.StatusInternalServerError, CodeInternal, "Internal Server Error", "")
}
