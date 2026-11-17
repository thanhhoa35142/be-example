package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/getaways"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

const (
	tierFree = "free"
	tierPlus = "plus"

	// tierQueryKey carries the resolved plan into the response-cache key. The
	// Tier middleware owns it: a client-supplied value is always discarded.
	tierQueryKey = "tier"
	tierCtxKey   = "getawaysTier"

	// hourQueryKey carries the current UTC hour into the cache key. Windows
	// depend on "now" (only hours from now on count), so a cached response
	// must never outlive its hour. Owned by the Tier middleware like tier.
	hourQueryKey = "hour"
	nowCtxKey    = "getawaysNow"
)

// GetawaysService is what the handler needs from getaways.Service.
type GetawaysService interface {
	List(ctx context.Context, q getaways.Query, now time.Time) (*getaways.Result, error)
}

// DeviceAuthenticator verifies a device id + secret pair.
type DeviceAuthenticator interface {
	Authenticate(ctx context.Context, id uuid.UUID, secret string) (bool, error)
}

// PlanResolver returns a device's plan ("free" or "plus").
type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

type GetawaysHandler struct {
	svc   GetawaysService
	auth  DeviceAuthenticator
	plans PlanResolver
	nowFn func() time.Time
}

func NewGetawaysHandler(svc GetawaysService, auth DeviceAuthenticator, plans PlanResolver) *GetawaysHandler {
	return &GetawaysHandler{svc: svc, auth: auth, plans: plans, nowFn: time.Now}
}

// Tier resolves the caller's plan before the response cache, so the cache key
// separates Free from Plus. The route is public: without both device headers,
// or with a wrong secret, the caller is Free. A client-supplied tier query
// parameter is discarded. Only a failing lookup is an error (500, never
// cached).
func (h *GetawaysHandler) Tier(c *gin.Context) {
	q := c.Request.URL.Query()
	q.Del(tierQueryKey)
	tier := tierFree

	rawID, secret := c.GetHeader(DeviceIDHeader), c.GetHeader(DeviceTokenHeader)
	if rawID != "" && secret != "" && h.auth != nil && h.plans != nil {
		if id, err := uuid.Parse(rawID); err == nil {
			ok, err := h.auth.Authenticate(c.Request.Context(), id, secret)
			if err != nil {
				httpx.WriteError(c, err)
				c.Abort()
				return
			}
			if ok {
				plan, err := h.plans.Plan(c.Request.Context(), id)
				if err != nil {
					httpx.WriteError(c, err)
					c.Abort()
					return
				}
				if plan == tierPlus {
					tier = tierPlus
				}
			}
		}
	}

	// The same instant keys the cache and feeds List, so an entry stored
	// under hour H never holds a result computed for hour H+1.
	now := h.nowFn().UTC()
	q.Del(hourQueryKey)
	q.Set(hourQueryKey, strconv.FormatInt(now.Truncate(time.Hour).Unix(), 10))
	q.Set(tierQueryKey, tier)
	c.Request.URL.RawQuery = q.Encode()
	c.Set(tierCtxKey, tier)
	c.Set(nowCtxKey, now)
	c.Next()
}

// requestNow is the instant Tier fixed for this request (the handler may also
// run without the middleware, e.g. in tests).
func (h *GetawaysHandler) requestNow(c *gin.Context) time.Time {
	if v, ok := c.Get(nowCtxKey); ok {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}
	return h.nowFn()
}

// GetawaysNormalize builds the cache key: known params only, defaults filled
// in, limit clamped per tier. Invalid values stay as sent so they key apart
// from valid ones and reach the handler, which answers 400.
func GetawaysNormalize(v url.Values) {
	keepOnly(v, "start", "days", "limit", tierQueryKey, hourQueryKey)
	if v.Get(tierQueryKey) != tierPlus {
		v.Set(tierQueryKey, tierFree)
	}
	if v.Get("days") == "" {
		v.Set("days", strconv.Itoa(getaways.DefaultDays))
	}
	limit := getaways.DefaultLimit
	raw := v.Get("limit")
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > getaways.MaxLimit {
			return
		}
		limit = n
	}
	if v.Get(tierQueryKey) != tierPlus && limit > getaways.FreeLimit {
		limit = getaways.FreeLimit
	}
	v.Set("limit", strconv.Itoa(limit))
}

func (h *GetawaysHandler) List(c *gin.Context) {
	q := getaways.Query{Plan: c.GetString(tierCtxKey)}

	if raw := c.Query("start"); raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidStart, "Invalid start", "expected a date as YYYY-MM-DD")
			return
		}
		q.Start = t
	}
	if raw := c.Query("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid days", "expected integer 1-3")
			return
		}
		if n == 0 {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidRange, "Invalid range", "days must be 1-3")
			return
		}
		q.Days = n
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid limit", "expected integer 1-39")
			return
		}
		if n == 0 {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid limit", "expected integer 1-39")
			return
		}
		q.Limit = n
	}

	res, err := h.svc.List(c.Request.Context(), q, h.requestNow(c))
	switch {
	case errors.Is(err, getaways.ErrInvalidStart):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidStart, "Invalid start", "start must be between yesterday and 6 days ahead (UTC)")
		return
	case errors.Is(err, getaways.ErrInvalidRange):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidRange, "Invalid range", "days must be 1-3 and the range must end within 6 days from today (UTC)")
		return
	case errors.Is(err, getaways.ErrInvalidLimit):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery, "Invalid limit", "expected integer 1-39")
		return
	case err != nil:
		httpx.WriteError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": toGetawaysResponse(res)})
}

type getawayReasonJSON struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params"`
}

type getawayItemJSON struct {
	Slug       string              `json:"slug"`
	Name       string              `json:"name"`
	Country    string              `json:"country"`
	TZ         string              `json:"tz"`
	Center     [2]float64          `json:"center"`
	WindowDays int                 `json:"windowDays"`
	BestDay    string              `json:"bestDay"`
	Window     getawayWindowJSON   `json:"window"`
	GoodHours  int                 `json:"goodHours"`
	DayScores  []getawayDayJSON    `json:"dayScores"`
	TopSpot    *getawaySpotJSON    `json:"topSpot"`
	Reasons    []getawayReasonJSON `json:"reasons"`
}

type getawayWindowJSON struct {
	Start        string `json:"start"`
	End          string `json:"end"`
	WeatherScore int    `json:"weatherScore"`
}

type getawayDayJSON struct {
	Date         string `json:"date"`
	WeatherScore *int   `json:"weatherScore"`
	GoodHours    int    `json:"goodHours"`
}

type getawaySpotJSON struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
	Score int     `json:"score"`
}

type getawaysJSON struct {
	Start      string            `json:"start"`
	Days       int               `json:"days"`
	Items      []getawayItemJSON `json:"items"`
	ComputedAt *string           `json:"computedAt"`
}

func toGetawaysResponse(r *getaways.Result) getawaysJSON {
	out := getawaysJSON{
		Start: r.Start.Format("2006-01-02"), Days: r.Days,
		Items: make([]getawayItemJSON, 0, len(r.Items)),
	}
	if r.ComputedAt != nil {
		s := r.ComputedAt.UTC().Format(time.RFC3339)
		out.ComputedAt = &s
	}
	for _, it := range r.Items {
		j := getawayItemJSON{
			Slug: it.Slug, Name: it.Name, Country: it.Country, TZ: it.TZ,
			Center:     [2]float64{it.Lon, it.Lat},
			WindowDays: it.WindowDays,
			BestDay:    it.BestDay.Format("2006-01-02"),
			Window: getawayWindowJSON{
				Start:        it.Window.Start.UTC().Format(time.RFC3339),
				End:          it.Window.End.UTC().Format(time.RFC3339),
				WeatherScore: it.Window.WeatherScore,
			},
			GoodHours: it.GoodHours,
			DayScores: make([]getawayDayJSON, 0, len(it.DayScores)),
			Reasons:   make([]getawayReasonJSON, 0, len(it.Reasons)),
		}
		for _, d := range it.DayScores {
			j.DayScores = append(j.DayScores, getawayDayJSON{Date: d.Date.Format("2006-01-02"), WeatherScore: d.WeatherScore, GoodHours: d.GoodHours})
		}
		if it.TopSpot != nil {
			j.TopSpot = &getawaySpotJSON{ID: it.TopSpot.ID.String(), Name: it.TopSpot.Name, Score: it.TopSpot.Score}
		}
		for _, r := range it.Reasons {
			p := r.Params
			if p == nil {
				p = map[string]any{}
			}
			j.Reasons = append(j.Reasons, getawayReasonJSON{Code: r.Code, Params: p})
		}
		out.Items = append(out.Items, j)
	}
	return out
}
