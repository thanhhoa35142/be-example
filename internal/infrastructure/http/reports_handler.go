package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/reports"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type ReportsHandler struct {
	svc *reports.Service
}

func NewReportsHandler(svc *reports.Service) *ReportsHandler {
	return &ReportsHandler{svc: svc}
}

type spotReportRequest struct {
	DeviceID  string  `json:"deviceId"`
	Type      string  `json:"type"`
	IssueCode *string `json:"issueCode"`
}

// POST /v1/spots/:id/reports — confirm / missing / closed / issue.
func (h *ReportsHandler) SpotReport(c *gin.Context) {
	spotID, ok := parseSpotID(c)
	if !ok {
		return
	}
	var req spotReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON report")
		return
	}
	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}

	err = h.svc.SubmitSpotReport(c.Request.Context(), reports.SpotReportInput{
		SpotID: spotID, DeviceID: deviceID, Type: req.Type, IssueCode: req.IssueCode,
	})
	h.writeError(c, err)
}

type crowdReportRequest struct {
	DeviceID string `json:"deviceId"`
	Level    int    `json:"level"`
}

// POST /v1/spots/:id/crowd-reports — busy / moderate / quiet for the current slot.
func (h *ReportsHandler) CrowdReport(c *gin.Context) {
	spotID, ok := parseSpotID(c)
	if !ok {
		return
	}
	var req crowdReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON crowd report")
		return
	}
	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}

	err = h.svc.SubmitCrowdReport(c.Request.Context(), spotID, deviceID, req.Level)
	h.writeError(c, err)
}

type scoreFeedbackRequest struct {
	DeviceID string    `json:"deviceId"`
	SpotID   string    `json:"spotId"`
	TSHour   time.Time `json:"tsHour"`
	Verdict  int       `json:"verdict"`
}

// POST /v1/scores/feedback — "is this score right?"
func (h *ReportsHandler) ScoreFeedback(c *gin.Context) {
	var req scoreFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON feedback")
		return
	}
	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}
	spotID, err := uuid.Parse(req.SpotID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid spotId", "expected a UUID")
		return
	}

	err = h.svc.SubmitScoreFeedback(c.Request.Context(), reports.ScoreFeedbackInput{
		SpotID: spotID, DeviceID: deviceID, TSHour: req.TSHour, Verdict: req.Verdict,
	})
	h.writeError(c, err)
}

func (h *ReportsHandler) writeError(c *gin.Context, err error) {
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"status": "accepted"}})
	case errors.Is(err, reports.ErrDuplicateReport):
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "duplicate"}})
	case errors.Is(err, reports.ErrDeviceNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "register the device first")
	case errors.Is(err, reports.ErrSpotNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Spot not found", "")
	case errors.Is(err, reports.ErrRateLimited):
		httpx.WriteProblem(c, http.StatusTooManyRequests, httpx.CodeRateLimited, "Rate limited", "too many reports, try later")
	case errors.Is(err, reports.ErrInvalidReport):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidReport, "Invalid report", "check type/level/verdict values")
	default:
		httpx.WriteError(c, err)
	}
}
