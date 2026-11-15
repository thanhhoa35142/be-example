package http

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/traffic"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// TrafficHandler serves the precomputed Search Console + GA4 rows that the
// google.sync worker writes. Reads only — the console never calls Google.
type TrafficHandler struct {
	svc *traffic.Service
}

func NewTrafficHandler(svc *traffic.Service) *TrafficHandler {
	return &TrafficHandler{svc: svc}
}

func trafficDays(c *gin.Context) int {
	days := 28
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	return days
}

// Gsc returns daily Search Console totals plus the top queries and pages
// over the synced window.
func (h *TrafficHandler) Gsc(c *gin.Context) {
	days := trafficDays(c)
	daily, err := h.svc.GscDaily(c.Request.Context(), days)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	queries, qWin, err := h.svc.GscTop(c.Request.Context(), "query", 25)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	pages, pWin, err := h.svc.GscTop(c.Request.Context(), "page", 25)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"days":       daily,
		"queries":    queries,
		"pages":      pages,
		"queryRange": qWin,
		"pageRange":  pWin,
	}})
}

// Ga4 returns daily GA4 totals plus window totals.
func (h *TrafficHandler) Ga4(c *gin.Context) {
	daily, totals, err := h.svc.Ga4Daily(c.Request.Context(), trafficDays(c))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"days":   daily,
		"totals": totals,
	}})
}
