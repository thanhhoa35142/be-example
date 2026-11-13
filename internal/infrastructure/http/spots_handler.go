package http

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type SpotsHandler struct {
	svc *spots.Service
}

func NewSpotsHandler(svc *spots.Service) *SpotsHandler {
	return &SpotsHandler{svc: svc}
}

// GET /v1/spots?bbox=west,south,east,north | ids=<uuid>,...
//
//	[&when=RFC3339][&from=RFC3339&hours=1..24][&include=area]
func (h *SpotsHandler) List(c *gin.Context) {
	q, perr := parseListQuery(c.Request.URL.Query())
	if perr != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, perr.code, perr.title, perr.detail)
		return
	}

	items, err := h.svc.List(c.Request.Context(), q)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toFeatureCollection(items, scoreHour(q, time.Now()), q.IncludeArea, q.Hours > 0))
}

// GET /v1/packs
func (h *SpotsHandler) Packs(c *gin.Context) {
	packs, err := h.svc.ListPacks(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": toPackItems(packs)}})
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

type queryProblem struct{ code, title, detail string }

// parseListQuery validates the GET /v1/spots query string. A malformed bbox
// used alone keeps its historical invalid_bbox code; every other selector /
// ids / hours / include problem is invalid_query.
func parseListQuery(v url.Values) (spots.ListQuery, *queryProblem) {
	var q spots.ListQuery
	invalid := func(detail string) (spots.ListQuery, *queryProblem) {
		return spots.ListQuery{}, &queryProblem{httpx.CodeInvalidQuery, "Invalid query", detail}
	}

	if v.Has("bbox") == v.Has("ids") {
		return invalid("provide exactly one of bbox or ids")
	}
	if v.Has("bbox") {
		bbox, err := parseBBox(v.Get("bbox"))
		if err != nil {
			return spots.ListQuery{}, &queryProblem{httpx.CodeInvalidBBox, "Invalid bbox", "expected bbox=west,south,east,north in WGS84 degrees"}
		}
		q.BBox = &bbox
	} else {
		parts := strings.Split(v.Get("ids"), ",")
		if len(parts) > spots.MaxBatchIDs {
			return invalid("ids accepts at most 50 ids")
		}
		for _, p := range parts {
			id, err := uuid.Parse(strings.TrimSpace(p))
			if err != nil {
				return invalid("ids must be comma-separated UUIDs")
			}
			q.IDs = append(q.IDs, id)
		}
	}

	for _, key := range []string{"when", "from"} {
		if raw := v.Get(key); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return spots.ListQuery{}, &queryProblem{httpx.CodeInvalidWhen, "Invalid " + key, "expected RFC3339 timestamp"}
			}
			if key == "when" {
				q.When = &t
			} else {
				q.From = &t
			}
		}
	}

	if v.Has("hours") {
		n, err := strconv.Atoi(v.Get("hours"))
		if err != nil || n < 1 || n > spots.MaxScoreHours {
			return invalid("hours must be an integer 1..24")
		}
		q.Hours = n
	}

	for _, inc := range strings.Split(v.Get("include"), ",") {
		switch strings.TrimSpace(inc) {
		case "":
		case "area":
			q.IncludeArea = true
		default:
			return invalid("unknown include value; supported: area")
		}
	}
	f, err := spots.ParseFilter(v.Get("kinds"), v.Get("amenities"), v.Get("minScore"))
	if err != nil {
		return invalid(err.Error())
	}
	q.Filter = f
	return q, nil
}

// scoreHour is the hour behind properties.score / properties.when; it mirrors
// spots.Service.List (when, else from, else now — truncated to the hour).
func scoreHour(q spots.ListQuery, now time.Time) time.Time {
	ts := now
	switch {
	case q.When != nil:
		ts = *q.When
	case q.From != nil:
		ts = *q.From
	}
	return ts.UTC().Truncate(time.Hour)
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
	case errors.Is(err, spots.ErrInvalidQuery):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid query", "")
	case errors.Is(err, spots.ErrBboxTooLarge):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeBboxTooLarge, "Bbox too large", "zoom in or reduce the viewport")
	default:
		httpx.WriteError(c, err)
	}
}
