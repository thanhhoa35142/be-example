package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// PlansHandler serves C2: device-owned plans, follows by share token and the
// anonymous token read.
type PlansHandler struct {
	svc *plans.Service
	now func() time.Time
}

func NewPlansHandler(svc *plans.Service) *PlansHandler {
	return &PlansHandler{svc: svc, now: time.Now}
}

type planRequest struct {
	SpotID uuid.UUID `json:"spotId" binding:"required"`
	Start  time.Time `json:"start" binding:"required"`
	End    time.Time `json:"end" binding:"required"`
	TZ     string    `json:"tz"`
}

// maxSharedReasons on the token view (worst hour of the window).
const maxSharedReasons = 3

func planDTO(p plans.Plan, now time.Time) gin.H {
	out := gin.H{
		"id":          p.ID,
		"role":        p.Role,
		"status":      p.Status(now),
		"shareToken":  p.ShareToken,
		"spot":        gin.H{"id": p.SpotID, "name": p.SpotName, "kind": p.SpotKind},
		"start":       p.Start,
		"end":         p.End,
		"tz":          p.TZ,
		"state":       p.State,
		"score":       p.Score,
		"capReason":   p.CapReason,
		"alternative": p.Alternative,
		"createdAt":   p.CreatedAt,
	}
	if p.Role == "owner" {
		out["baselineScore"] = p.Baseline
		out["followerCount"] = p.FollowerCount
	}
	return out
}

func (h *PlansHandler) Create(c *gin.Context) {
	var req planRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body",
			"expected {spotId, start, end, tz} with RFC 3339 times")
		return
	}
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	p, err := h.svc.Create(c.Request.Context(), deviceID, plans.CreateInput{
		SpotID: req.SpotID, Start: req.Start, End: req.End, TZ: req.TZ,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": planDTO(*p, h.now())})
}

func (h *PlansHandler) List(c *gin.Context) {
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	list, err := h.svc.List(c.Request.Context(), deviceID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	now := h.now()
	items := make([]gin.H, 0, len(list))
	for _, p := range list {
		items = append(items, planDTO(p, now))
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": items}})
}

func (h *PlansHandler) Cancel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return
	}
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), id, deviceID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *PlansHandler) Follow(c *gin.Context) {
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	p, err := h.svc.Follow(c.Request.Context(), c.Param("token"), deviceID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": planDTO(*p, h.now())})
}

func (h *PlansHandler) Unfollow(c *gin.Context) {
	deviceID, ok := requestDeviceID(c)
	if !ok {
		return
	}
	if err := h.svc.Unfollow(c.Request.Context(), c.Param("token"), deviceID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// sharedHeaders: the token view is per-link, never indexed and only briefly
// cached (the score moves with each recompute).
func sharedHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, max-age=60")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Referrer-Policy", "no-referrer")
}

func sharedReasons(w plans.Window) []spots.Reason {
	r := w.Worst.Reasons
	if len(r) > maxSharedReasons {
		r = r[:maxSharedReasons]
	}
	if r == nil {
		r = []spots.Reason{}
	}
	return r
}

// Shared is the anonymous read by token: one window, no device or follower
// data, no alternative. Unknown, canceled and ended plans are one 404.
func (h *PlansHandler) Shared(c *gin.Context) {
	sharedHeaders(c)
	sh, err := h.svc.Shared(c.Request.Context(), c.Param("token"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	var score *int
	var capReason *string
	reasons := []spots.Reason{}
	if sh.HasWindow {
		score, capReason, reasons = &sh.Window.Mean, sh.Window.Cap, sharedReasons(sh.Window)
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"spot":      gin.H{"id": sh.SpotID, "name": sh.SpotName, "kind": sh.SpotKind},
		"start":     sh.Start,
		"end":       sh.End,
		"tz":        sh.TZ,
		"state":     sh.State,
		"score":     score,
		"capReason": capReason,
		"reasons":   reasons,
	}})
}

func (h *PlansHandler) writeError(c *gin.Context, err error) {
	var ve plans.ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteProblem(c, http.StatusBadRequest, ve.Code, "Invalid plan", "")
	case errors.Is(err, plans.ErrNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Plan not found", "")
	case errors.Is(err, plans.ErrSpotNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Spot not found", "spotId does not reference an active spot")
	case errors.Is(err, plans.ErrDeviceInvalid):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Device not found", "")
	case errors.Is(err, plans.ErrPlanLimit):
		httpx.WriteProblem(c, http.StatusForbidden, httpx.CodePlanLimit, "Plan limit reached", "")
	case errors.Is(err, plans.ErrFollowerLimit):
		httpx.WriteProblem(c, http.StatusConflict, httpx.CodeFollowerLimit, "Follower limit reached", "")
	case errors.Is(err, plans.ErrFollowLimit):
		httpx.WriteProblem(c, http.StatusForbidden, httpx.CodeFollowLimit, "Follow limit reached", "")
	case errors.Is(err, plans.ErrOwnPlan):
		httpx.WriteProblem(c, http.StatusConflict, httpx.CodeOwnPlan, "Own plan", "")
	default:
		httpx.WriteError(c, err)
	}
}
