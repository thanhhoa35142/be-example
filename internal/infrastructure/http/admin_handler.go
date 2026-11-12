package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type AdminHandler struct {
	svc *admin.Service
}

func NewAdminHandler(svc *admin.Service) *AdminHandler {
	return &AdminHandler{svc: svc}
}

// GET /v1/admin/overview — headline counts.
func (h *AdminHandler) Overview(c *gin.Context) {
	o, err := h.svc.Overview(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": o})
}

// GET /v1/admin/freshness — newest timestamp per pipeline stage.
func (h *AdminHandler) Freshness(c *gin.Context) {
	f, err := h.svc.Freshness(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": f})
}

// parseFilter reads range + filter query params shared by metrics endpoints.
// Supports `from`/`to` (RFC3339) or `days` (trailing window ending now).
func parseFilter(c *gin.Context) (admin.MetricsFilter, error) {
	var f admin.MetricsFilter
	parseTime := func(key string) (time.Time, error) {
		v := c.Query(key)
		if v == "" {
			return time.Time{}, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must be RFC3339", key)
		}
		return t.UTC(), nil
	}
	var err error
	if f.From, err = parseTime("from"); err != nil {
		return f, err
	}
	if f.To, err = parseTime("to"); err != nil {
		return f, err
	}
	if days := c.Query("days"); days != "" {
		d, err := strconv.Atoi(days)
		if err != nil || d <= 0 {
			return f, fmt.Errorf("days must be a positive integer")
		}
		f.To = time.Now().UTC()
		f.From = f.To.Add(-time.Duration(d) * 24 * time.Hour)
	}
	if v := c.Query("cityPackId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, fmt.Errorf("cityPackId must be a UUID")
		}
		f.CityPackID = &id
	}
	if v := c.Query("kind"); v != "" {
		f.Kind = &v
	}
	if v := c.Query("spotId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, fmt.Errorf("spotId must be a UUID")
		}
		f.SpotID = &id
	}
	if v := c.Query("type"); v != "" {
		f.ReportType = &v
	}
	if v := c.Query("status"); v != "" {
		f.ReportStatus = &v
	}
	if v := c.Query("name"); v != "" {
		f.EventName = &v
	}
	return f, nil
}

// GET /v1/admin/metrics — analytical counters. Query: from,to (RFC3339) or
// days; filters cityPackId, kind, spotId, type, status, name.
func (h *AdminHandler) Metrics(c *gin.Context) {
	f, err := parseFilter(c)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", err.Error())
		return
	}
	m, err := h.svc.Metrics(c.Request.Context(), f)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": m})
}

// GET /v1/admin/events?days=30&name=&from=&to= — event counts by name/day.
func (h *AdminHandler) Events(c *gin.Context) {
	f, err := parseFilter(c)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", err.Error())
		return
	}
	s, err := h.svc.EventSummary(c.Request.Context(), f)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": s})
}

// GET /v1/admin/jobs?limit=&cursor= — recent job_run rows, keyset-paginated
// (cursor = last job run id).
func (h *AdminHandler) Jobs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	var beforeID *int64
	if raw := c.Query("cursor"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid cursor", "expected a job run id")
			return
		}
		beforeID = &id
	}
	page, err := h.svc.Jobs(c.Request.Context(), limit, beforeID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/jobs/schedules — intervals for registered worker jobs.
func (h *AdminHandler) JobSchedules(c *gin.Context) {
	schedules, err := h.svc.JobSchedules(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": schedules}})
}

type updateJobScheduleRequest struct {
	IntervalSeconds int64 `json:"intervalSeconds"`
}

// PATCH /v1/admin/jobs/:job/schedule — update a registered job interval.
func (h *AdminHandler) UpdateJobSchedule(c *gin.Context) {
	var req updateJobScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected intervalSeconds")
		return
	}
	schedule, err := h.svc.UpdateJobSchedule(c.Request.Context(), c.Param("job"), req.IntervalSeconds)
	if err != nil {
		switch {
		case errors.Is(err, admin.ErrInvalidJobSchedule):
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid job schedule", err.Error())
		case errors.Is(err, admin.ErrJobScheduleNotFound):
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Job schedule not found", "")
		default:
			httpx.WriteError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": schedule})
}

// GET /v1/admin/reports?status=pending&limit=&cursor= — moderation queue,
// keyset-paginated (cursor = "<unixnano>|<reportId>" from nextCursor).
func (h *AdminHandler) Reports(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	var after *admin.ReportCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := admin.ParseReportCursor(raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid cursor", "malformed pagination cursor")
			return
		}
		after = &cur
	}
	page, err := h.svc.PendingReports(c.Request.Context(), limit, after)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

type moderateRequest struct {
	Status string `json:"status"`
}

// PATCH /v1/admin/reports/:id — accept or reject a spot report.
func (h *AdminHandler) ModerateReport(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	var req moderateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected {\"status\":\"accepted|rejected\"}")
		return
	}
	if err := h.svc.ModerateReport(c.Request.Context(), id, req.Status); err != nil {
		switch {
		case errors.Is(err, admin.ErrInvalidStatus):
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid status", "expected accepted or rejected")
		case errors.Is(err, admin.ErrReportNotFound):
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Report not found", "")
		default:
			httpx.WriteError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": req.Status}})
}

// GET /v1/admin/scoring-config — all versions.
func (h *AdminHandler) ScoringConfigs(c *gin.Context) {
	cfgs, err := h.svc.ListScoringConfigs(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": cfgs}})
}

type scoringConfigRequest struct {
	Weights    json.RawMessage `json:"weights"`
	Thresholds json.RawMessage `json:"thresholds"`
	Activate   bool            `json:"activate"`
}

// POST /v1/admin/scoring-config — create a new version (draft or active).
func (h *AdminHandler) CreateScoringConfig(c *gin.Context) {
	var req scoringConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected weights + thresholds JSON objects")
		return
	}
	cfg, err := h.svc.CreateScoringConfig(c.Request.Context(), admin.ScoringConfigInput{
		Weights: req.Weights, Thresholds: req.Thresholds, Activate: req.Activate,
	})
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": cfg})
}

// POST /v1/admin/scoring-config/:version/activate — make a version live.
func (h *AdminHandler) ActivateScoringConfig(c *gin.Context) {
	v, err := strconv.ParseInt(c.Param("version"), 10, 16)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid version", "expected an integer")
		return
	}
	if err := h.svc.ActivateScoringConfig(c.Request.Context(), int16(v)); err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"version": v, "activeFrom": time.Now().UTC()}})
}

func (h *AdminHandler) writeConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, admin.ErrInvalidFilter):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid filter", err.Error())
	case errors.Is(err, admin.ErrInvalidConfig):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid scoring config", err.Error())
	case errors.Is(err, admin.ErrConfigNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Scoring config not found", "")
	default:
		httpx.WriteError(c, err)
	}
}
