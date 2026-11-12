package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gitlab.com/walkalike-group/pickhour/internal/modules/spots"
	"gitlab.com/walkalike-group/pickhour/internal/platform/httpx"
)

type SpotsHandler struct {
	svc *spots.Service
}

func NewSpotsHandler(svc *spots.Service) *SpotsHandler {
	return &SpotsHandler{svc: svc}
}

// GET /v1/spots?bbox=west,south,east,north&when=RFC3339
func (h *SpotsHandler) List(c *gin.Context) {
	bbox, err := parseBBox(c.Query("bbox"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBBox, "Invalid bbox", "expected bbox=west,south,east,north in WGS84 degrees")
		return
	}

	var when *time.Time
	if raw := c.Query("when"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidWhen, "Invalid when", "expected RFC3339 timestamp")
			return
		}
		when = &t
	}

	items, err := h.svc.ListInBBox(c.Request.Context(), bbox, when)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}

	ts := time.Now().UTC()
	if when != nil {
		ts = when.UTC()
	}
	c.JSON(http.StatusOK, toFeatureCollection(items, ts.Truncate(time.Hour)))
}

// GET /v1/spots/{id}
func (h *SpotsHandler) Get(c *gin.Context) {
	id, ok := parseSpotID(c)
	if !ok {
		return
	}
	detail, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": toDetailResponse(detail)})
}

// GET /v1/spots/{id}/forecast
func (h *SpotsHandler) Forecast(c *gin.Context) {
	id, ok := parseSpotID(c)
	if !ok {
		return
	}
	points, err := h.svc.Forecast(c.Request.Context(), id)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": points})
}

func parseSpotID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidID, "Invalid id", "expected a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func parseBBox(raw string) (spots.BBox, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return spots.BBox{}, errors.New("bad format")
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return spots.BBox{}, err
		}
		vals[i] = v
	}
	return spots.BBox{West: vals[0], South: vals[1], East: vals[2], North: vals[3]}, nil
}

func (h *SpotsHandler) writeServiceError(c *gin.Context, err error) {
	var merged spots.MergedError
	switch {
	case errors.As(err, &merged):
		c.Header("Location", "/v1/spots/"+merged.NewID.String())
		c.JSON(http.StatusMovedPermanently, gin.H{"id": merged.NewID.String()})
	case errors.Is(err, spots.ErrNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Spot not found", "")
	case errors.Is(err, spots.ErrInvalidBBox):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBBox, "Invalid bbox", "west<east, south<north, within WGS84 bounds")
	case errors.Is(err, spots.ErrBboxTooLarge):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeBboxTooLarge, "Bbox too large", "zoom in or reduce the viewport")
	default:
		httpx.WriteError(c, err)
	}
}
