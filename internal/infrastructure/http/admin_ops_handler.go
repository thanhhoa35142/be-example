package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// opsLimit parses ?limit= with the same default/clamp the service applies.
func opsLimit(c *gin.Context) int {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	return limit
}

// reportCursor parses the shared (created_at,id) keyset cursor used by the
// ops lists (reports history, crowd reports, feedbacks, devices, deliveries,
// apple notifications).
func reportCursor(c *gin.Context) (*admin.ReportCursor, bool) {
	raw := c.Query("cursor")
	if raw == "" {
		return nil, true
	}
	cur, err := admin.ParseReportCursor(raw)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid cursor", "malformed pagination cursor")
		return nil, false
	}
	return &cur, true
}

// parseRange reads optional RFC3339 from/to query params.
func parseRange(c *gin.Context) (time.Time, time.Time, bool) {
	parse := func(key string) (time.Time, bool) {
		v := c.Query(key)
		if v == "" {
			return time.Time{}, true
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", key+" must be RFC3339")
			return time.Time{}, false
		}
		return t.UTC(), true
	}
	from, ok := parse("from")
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	to, ok := parse("to")
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func uuidQuery(c *gin.Context, key string) (*uuid.UUID, bool) {
	v := c.Query(key)
	if v == "" {
		return nil, true
	}
	id, err := uuid.Parse(v)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", key+" must be a UUID")
		return nil, false
	}
	return &id, true
}

func intQuery(c *gin.Context, key string, min, max int) (*int, bool) {
	v := c.Query(key)
	if v == "" {
		return nil, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", key+" out of range")
		return nil, false
	}
	return &n, true
}

func int64Query(c *gin.Context, key string) (*int64, bool) {
	v := c.Query(key)
	if v == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", key+" must be a positive integer")
		return nil, false
	}
	return &n, true
}

// GET /v1/admin/reports/history — full moderation history with filters.
func (h *AdminHandler) SpotReportsHistory(c *gin.Context) {
	spotID, ok := uuidQuery(c, "spotId")
	if !ok {
		return
	}
	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.SpotReports(c.Request.Context(), admin.SpotReportFilter{
		Status: c.Query("status"), Type: c.Query("type"),
		SpotID: spotID, From: from, To: to,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/reports/:id — single report detail.
func (h *AdminHandler) SpotReportDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	rep, err := h.svc.SpotReportDetail(c.Request.Context(), id)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rep})
}

// GET /v1/admin/crowd-reports
func (h *AdminHandler) CrowdReports(c *gin.Context) {
	spotID, ok := uuidQuery(c, "spotId")
	if !ok {
		return
	}
	deviceID, ok := uuidQuery(c, "deviceId")
	if !ok {
		return
	}
	level, ok := intQuery(c, "level", 1, 3)
	if !ok {
		return
	}
	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.CrowdReports(c.Request.Context(), admin.CrowdReportFilter{
		SpotID: spotID, DeviceID: deviceID, Level: level, From: from, To: to,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/score-feedbacks
func (h *AdminHandler) ScoreFeedbacks(c *gin.Context) {
	spotID, ok := uuidQuery(c, "spotId")
	if !ok {
		return
	}
	deviceID, ok := uuidQuery(c, "deviceId")
	if !ok {
		return
	}
	verdict, ok := intQuery(c, "verdict", -1, 1)
	if !ok {
		return
	}
	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.ScoreFeedbacks(c.Request.Context(), admin.ScoreFeedbackFilter{
		SpotID: spotID, DeviceID: deviceID, Verdict: verdict, From: from, To: to,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/devices?id=&pushStatus=&includeDeleted=
func (h *AdminHandler) Devices(c *gin.Context) {
	id, ok := uuidQuery(c, "id")
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.Devices(c.Request.Context(), admin.AdminDeviceFilter{
		ID:             id,
		PushStatus:     c.Query("pushStatus"),
		IncludeDeleted: c.Query("includeDeleted") == "true",
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/devices/:id — device + linked subscriptions + plan.
func (h *AdminHandler) DeviceDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	d, err := h.svc.DeviceDetail(c.Request.Context(), id)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// GET /v1/admin/alert-deliveries?status=&ruleId=&deviceId=&apnsStatus=&from=&to=
func (h *AdminHandler) AlertDeliveries(c *gin.Context) {
	ruleID, ok := uuidQuery(c, "ruleId")
	if !ok {
		return
	}
	deviceID, ok := uuidQuery(c, "deviceId")
	if !ok {
		return
	}
	apnsStatus, ok := intQuery(c, "apnsStatus", -1, 65535)
	if !ok {
		return
	}
	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.AlertDeliveries(c.Request.Context(), admin.DeliveryFilter{
		Status: c.Query("status"), RuleID: ruleID, DeviceID: deviceID,
		APNsStatus: apnsStatus, From: from, To: to,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/apple-notifications?status=&type=&environment=&txn=
func (h *AdminHandler) AppleNotifications(c *gin.Context) {
	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	before, ok := reportCursor(c)
	if !ok {
		return
	}
	page, err := h.svc.AppleNotifications(c.Request.Context(), admin.AppleNotificationFilter{
		Status:      c.Query("status"),
		Type:        c.Query("type"),
		Environment: c.Query("environment"),
		Txn:         c.Query("txn"),
		From:        from, To: to,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// GET /v1/admin/providers/health — provider quota usage + recent
// weather.refresh job runs.
func (h *AdminHandler) ProviderHealth(c *gin.Context) {
	health, err := h.svc.ProviderHealth(c.Request.Context())
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": health})
}

// GET /v1/admin/ingest/features?pack=&role=&source=&runId=
func (h *AdminHandler) IngestFeatures(c *gin.Context) {
	runID, ok := int64Query(c, "runId")
	if !ok {
		return
	}
	raw := c.Query("cursor")
	var before *admin.IngestFeatureCursor
	if raw != "" {
		cur, err := admin.ParseIngestFeatureCursor(raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid cursor", "malformed pagination cursor")
			return
		}
		before = &cur
	}
	page, err := h.svc.IngestFeatures(c.Request.Context(), admin.IngestFeatureFilter{
		PackSlug: c.Query("pack"), Role: c.Query("role"),
		Source: c.Query("source"), RunID: runID,
	}, opsLimit(c), before)
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// POST /v1/admin/ingest/packs/:slug/run — ask the worker to ingest one pack.
func (h *AdminHandler) RunPackIngest(c *gin.Context) {
	schedule, err := h.svc.RequestPackRun(c.Request.Context(), c.Param("slug"))
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": schedule})
}

func (h *AdminHandler) writeOpsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, admin.ErrInvalidOpsFilter):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid filter", err.Error())
	case errors.Is(err, admin.ErrDeviceNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	case errors.Is(err, admin.ErrInvalidSpotPatch):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid spot patch", err.Error())
	case errors.Is(err, admin.ErrInvalidPackSlug):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid pack slug", "expected a city pack slug")
	case errors.Is(err, admin.ErrReportNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Report not found", "")
	case errors.Is(err, admin.ErrCityPackNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "City pack not found", "")
	case errors.Is(err, admin.ErrInvalidCursor):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid cursor", "malformed pagination cursor")
	default:
		h.writeConsoleError(c, err)
	}
}
