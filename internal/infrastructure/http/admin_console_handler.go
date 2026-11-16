package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

func (h *AdminHandler) RunJobNow(c *gin.Context) {
	schedule, err := h.svc.RequestJobRun(c.Request.Context(), c.Param("job"))
	if err != nil {
		if errors.Is(err, admin.ErrJobScheduleNotFound) {
			httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Job schedule not found", "")
			return
		}
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": schedule})
}

func (h *AdminHandler) PlanLimits(c *gin.Context) {
	limits, err := h.svc.PlanLimits(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": limits}})
}

type planLimitRequest struct {
	Limit *int `json:"limit"`
}

func (h *AdminHandler) UpdatePlanLimit(c *gin.Context) {
	var req planLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Limit == nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected {\"limit\": integer}")
		return
	}
	limit, err := h.svc.UpdatePlanLimit(c.Request.Context(), c.Param("plan"), c.Param("feature"), *req.Limit)
	if err != nil {
		h.writeConsoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": limit})
}

func (h *AdminHandler) CityPacks(c *gin.Context) {
	packs, err := h.svc.CityPacks(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": packs}})
}

func (h *AdminHandler) CityPackDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	d, err := h.svc.CityPackDetail(c.Request.Context(), id)
	if err != nil {
		h.writeConsoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

type statusRequest struct {
	Status string `json:"status"`
}

func (h *AdminHandler) UpdateCityPack(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Status == "" {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected {\"status\": \"draft|beta|live|retired\"}")
		return
	}
	changed, err := h.svc.SetCityPackStatus(c.Request.Context(), id, req.Status)
	if err != nil {
		h.writeConsoleError(c, err)
		return
	}
	if changed {
		h.bumpPublicCache(c)
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": req.Status}})
}

func (h *AdminHandler) Spots(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	spots, err := h.svc.SearchSpots(c.Request.Context(), admin.SpotSearch{
		Query:    c.Query("q"),
		Status:   c.Query("status"),
		PackSlug: c.Query("pack"),
		Limit:    limit,
	})
	if err != nil {
		h.writeConsoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": spots}})
}

func (h *AdminHandler) SpotDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	detail, err := h.svc.SpotDetail(c.Request.Context(), id)
	if err != nil {
		h.writeConsoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": detail})
}

type spotPatchRequest struct {
	Action   string     `json:"action"`
	TargetID *uuid.UUID `json:"targetId"`
	Name     *string    `json:"name"`
	Curated  *bool      `json:"curated"`
}

// UpdateSpot handles both status actions (hide/restore/approve/merge/
// unmerge) and field edits (name, curated) on PATCH /v1/admin/spots/:id.
func (h *AdminHandler) UpdateSpot(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	var req spotPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body",
			"expected {\"action\":\"hide|restore|approve|merge|unmerge\"} or {\"name\":...} or {\"curated\":...}")
		return
	}
	result, err := h.svc.UpdateSpot(c.Request.Context(), id, admin.SpotPatch{
		Action: req.Action, TargetID: req.TargetID, Name: req.Name, Curated: req.Curated,
	})
	if err != nil {
		h.writeOpsError(c, err)
		return
	}
	if result.Changed {
		h.bumpPublicCache(c)
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// bumpPublicCache invalidates the public read caches (spot lists/details,
// forecasts) after an admin mutation that changes what users see. Best-effort:
// a Redis failure must not fail the mutation itself.
func (h *AdminHandler) bumpPublicCache(c *gin.Context) {
	if h.rdb == nil {
		return
	}
	ctx := c.Request.Context()
	_ = cache.Bump(ctx, h.rdb)
	_ = cache.BumpWeather(ctx, h.rdb)
}

func (h *AdminHandler) writeConsoleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, admin.ErrInvalidPlanLimit):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid plan limit", err.Error())
	case errors.Is(err, admin.ErrInvalidSpotAction):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid action", "expected hide, restore, approve, merge or unmerge")
	case errors.Is(err, admin.ErrInvalidSpotQuery):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", err.Error())
	case errors.Is(err, admin.ErrPlanLimitNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Plan limit not found", "")
	case errors.Is(err, admin.ErrCityPackNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "City pack not found", "")
	case errors.Is(err, admin.ErrSpotNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Spot not found", "")
	case errors.Is(err, admin.ErrInvalidTransition):
		httpx.WriteProblem(c, http.StatusConflict, httpx.CodeConflict, "Status change not allowed", err.Error())
	default:
		httpx.WriteError(c, err)
	}
}
