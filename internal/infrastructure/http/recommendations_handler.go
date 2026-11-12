package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type RecommendationsHandler struct {
	svc *recommendations.Service
}

func NewRecommendationsHandler(svc *recommendations.Service) *RecommendationsHandler {
	return &RecommendationsHandler{svc: svc}
}

// GET /v1/recommendations?lat=&lon=&radius=&when=&limit=
func (h *RecommendationsHandler) List(c *gin.Context) {
	lat, err := strconv.ParseFloat(c.Query("lat"), 64)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid lat", "expected decimal degrees")
		return
	}
	lon, err := strconv.ParseFloat(c.Query("lon"), 64)
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid lon", "expected decimal degrees")
		return
	}

	q := recommendations.Query{Lat: lat, Lon: lon, When: time.Now().UTC()}

	if raw := c.Query("radius"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid radius", "expected meters")
			return
		}
		q.RadiusM = v
	}
	if raw := c.Query("when"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidWhen, "Invalid when", "expected RFC3339 timestamp")
			return
		}
		q.When = t
	}
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid limit", "expected integer")
			return
		}
		q.Limit = v
	}

	items, err := h.svc.Recommend(c.Request.Context(), q)
	if err != nil {
		if err == recommendations.ErrInvalidQuery {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", "check lat/lon/radius bounds")
			return
		}
		httpx.WriteError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"when":  q.When.UTC().Format(time.RFC3339),
			"items": toRecommendationItems(items),
		},
	})
}
