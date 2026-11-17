package http

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/weather"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// NearbyHours is how far ahead the near-me forecast reaches. 48h covers
// "today + tomorrow" planning without paying for the full 8-day window.
const NearbyHours = 48

type WeatherHandler struct {
	provider weather.Provider
}

func NewWeatherHandler(provider weather.Provider) *WeatherHandler {
	return &WeatherHandler{provider: provider}
}

type nearbyPoint struct {
	TS         time.Time `json:"ts"`
	TempC      *float64  `json:"tempC,omitempty"`
	FeelsLikeC *float64  `json:"feelsLikeC,omitempty"`
	PrecipProb *int      `json:"precipProb,omitempty"`
	WindKmh    *float64  `json:"windKmh,omitempty"`
	UV         *float64  `json:"uv,omitempty"`
	Cloud      *int      `json:"cloud,omitempty"`
}

// Nearby returns an hourly forecast for an arbitrary coordinate by calling
// the configured weather provider (public Open-Meteo by default,
// OPEN_METEO_URL when self-hosted). Nothing is persisted and coordinates
// are never logged — the cache key uses a rounded position.
func (h *WeatherHandler) Nearby(c *gin.Context) {
	lat, err := strconv.ParseFloat(c.Query("lat"), 64)
	if err != nil || math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery,
			"Invalid query", "lat must be a number in [-90, 90]")
		return
	}
	lng, err := strconv.ParseFloat(c.Query("lng"), 64)
	if err != nil || math.IsNaN(lng) || math.IsInf(lng, 0) || lng < -180 || lng > 180 {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidQuery,
			"Invalid query", "lng must be a number in [-180, 180]")
		return
	}

	now := time.Now().UTC()
	points, err := h.provider.Forecast(c.Request.Context(), lat, lng, now, now.Add(NearbyHours*time.Hour))
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	out := make([]nearbyPoint, 0, len(points))
	for _, p := range points {
		out = append(out, nearbyPoint{
			TS:         p.TS,
			TempC:      p.TempC,
			FeelsLikeC: p.FeelsLikeC,
			PrecipProb: p.PrecipProb,
			WindKmh:    p.WindKmh,
			UV:         p.UV,
			Cloud:      p.Cloud,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}
