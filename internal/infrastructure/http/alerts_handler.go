package http

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type AlertsHandler struct {
	svc *alerts.Service
}

func NewAlertsHandler(svc *alerts.Service) *AlertsHandler {
	return &AlertsHandler{svc: svc}
}

type alertRuleRequest struct {
	DeviceID   string     `json:"deviceId"`
	SpotID     *uuid.UUID `json:"spotId"`
	H3Cell     *uint64    `json:"h3Cell"`
	Area       *areaPoint `json:"area"`
	RadiusM    *int       `json:"radiusM"`
	MinScore   int        `json:"minScore"`
	DaysMask   int        `json:"daysMask"`
	StartLocal string     `json:"startLocal"`
	EndLocal   string     `json:"endLocal"`
	TZ         string     `json:"tz"`
	Enabled    *bool      `json:"enabled"`
}

// areaPoint is a coarse client point; both fields are required (pointers so
// a missing one is distinguishable from 0).
type areaPoint struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

func round3(v float64) float64 {
	r := math.Round(v*1000) / 1000
	if r == 0 {
		return 0
	}
	return r
}

func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d/time.Hour)%24, int(d/time.Minute)%60)
}

func ruleDTO(r alerts.Rule) gin.H {
	// areaCenter: cell center rounded to 3 decimals for area rules, null for
	// spot rules.
	var center gin.H
	if lat, lon, ok := r.AreaCenter(); ok {
		center = gin.H{"lat": round3(lat), "lon": round3(lon)}
	}
	return gin.H{
		"areaCenter": center,
		"id":         r.ID,
		"deviceId":   r.DeviceID,
		"spotId":     r.SpotID,
		"h3Cell":     r.H3Cell,
		"radiusM":    r.RadiusM,
		"minScore":   r.MinScore,
		"daysMask":   r.DaysMask,
		"startLocal": hhmm(r.StartLocal),
		"endLocal":   hhmm(r.EndLocal),
		"tz":         r.TZ,
		"enabled":    r.Enabled,
	}
}

func (h *AlertsHandler) input(req alertRuleRequest) (alerts.RuleInput, bool) {
	var area *alerts.LatLon
	if req.Area != nil {
		if req.Area.Lat == nil || req.Area.Lon == nil {
			return alerts.RuleInput{}, false
		}
		area = &alerts.LatLon{Lat: *req.Area.Lat, Lon: *req.Area.Lon}
	}
	return alerts.RuleInput{
		SpotID: req.SpotID, H3Cell: req.H3Cell, Area: area, RadiusM: req.RadiusM,
		MinScore: req.MinScore, DaysMask: req.DaysMask,
		StartLocal: req.StartLocal, EndLocal: req.EndLocal,
		TZ: req.TZ, Enabled: req.Enabled,
	}, true
}

// GET /v1/alerts?deviceId= — list rules for a device.
func (h *AlertsHandler) List(c *gin.Context) {
	deviceID, err := uuid.Parse(c.Query("deviceId"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", "deviceId must be a UUID")
		return
	}
	rules, err := h.svc.List(c.Request.Context(), deviceID)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	items := make([]gin.H, 0, len(rules))
	for _, r := range rules {
		items = append(items, ruleDTO(r))
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": items}})
}

// POST /v1/alerts — create a rule (plan-limited).
func (h *AlertsHandler) Create(c *gin.Context) {
	var req alertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON alert rule")
		return
	}
	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}
	in, ok := h.input(req)
	if !ok {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid rule", "area needs lat and lon")
		return
	}
	rule, err := h.svc.Create(c.Request.Context(), deviceID, in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": ruleDTO(*rule)})
}

// PUT /v1/alerts/:id — replace a rule.
func (h *AlertsHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	var req alertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected JSON alert rule")
		return
	}
	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid deviceId", "expected a UUID")
		return
	}
	in, ok := h.input(req)
	if !ok {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid rule", "area needs lat and lon")
		return
	}
	rule, err := h.svc.Update(c.Request.Context(), id, deviceID, in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ruleDTO(*rule)})
}

// DELETE /v1/alerts/:id?deviceId= — remove a rule.
func (h *AlertsHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	deviceID, err := uuid.Parse(c.Query("deviceId"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", "deviceId must be a UUID")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id, deviceID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AlertsHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, alerts.ErrNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Alert rule not found", "")
	case errors.Is(err, alerts.ErrInvalidRule):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid rule", err.Error())
	case errors.Is(err, alerts.ErrPlanLimit):
		httpx.WriteProblem(c, http.StatusForbidden, httpx.CodePlanLimit, "Plan limit reached", err.Error())
	case errors.Is(err, alerts.ErrDeviceInvalid):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	default:
		httpx.WriteError(c, err)
	}
}
