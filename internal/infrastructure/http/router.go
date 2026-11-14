package http

import (
	"fmt"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

// Options configures the HTTP edge. Zero values fall back to safe defaults.
type Options struct {
	// TrustedProxies are the IPs/CIDRs whose X-Forwarded-For is believed.
	// Default (empty): trust none, ClientIP is the socket peer. Behind a load
	// balancer set this to the LB's address, otherwise every client shares the
	// LB's rate-limit bucket.
	TrustedProxies []string
	// MaxBodyBytes is the default request body cap (default 64 KiB).
	MaxBodyBytes int64
	// Limits are the per-IP rate budgets (default DefaultRateLimits()).
	Limits *RateLimits
	// AdminToken enables /v1/admin when non-empty.
	AdminToken string
	// AdminFailed rate-limits failed admin auth per IP (default 10/min).
	AdminFailed *ratelimit.Rule
	// LimiterFor builds rate-limit checkers per (rule, namespace). Nil =
	// in-memory limiters; set to ratelimit.NewRedis for shared buckets.
	LimiterFor func(ratelimit.Rule, string) ratelimit.Checker
	// Cache is the Redis client for GET response caching. Nil disables it.
	Cache *redis.Client
}

const (
	defaultMaxBody = 64 << 10
	// Routes that legitimately carry more than the default.
	maxEventsBody   = 512 << 10 // 100 events x 4 KiB props + envelope
	maxAppStoreBody = 256 << 10 // JWS with x5c chain + nested transaction
)

type Handlers struct {
	Spots           *SpotsHandler
	Recommendations *RecommendationsHandler
	Devices         *DevicesHandler
	Reports         *ReportsHandler
	Alerts          *AlertsHandler
	Entitlement     *EntitlementHandler
	Admin           *AdminHandler
	Events          *EventsHandler
	Digest          *DigestHandler
	Share           *ShareHandler
}

// NewRouter builds the engine. Call gin.SetMode (config.ConfigureGinMode)
// before this: Gin reads the mode when the engine is created.
func NewRouter(pool *pgxpool.Pool, h Handlers, opts Options) (*gin.Engine, error) {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBody
	}
	limits := DefaultRateLimits()
	if opts.Limits != nil {
		limits = *opts.Limits
	}
	adminFail := ratelimit.PerMinute(10, 10)
	if opts.AdminFailed != nil {
		adminFail = *opts.AdminFailed
	}
	limiterFor := opts.LimiterFor
	if limiterFor == nil {
		limiterFor = func(r ratelimit.Rule, _ string) ratelimit.Checker { return ratelimit.New(r) }
	}

	r := gin.New()
	// Gin trusts every proxy by default, which makes ClientIP() spoofable via
	// X-Forwarded-For. nil = trust none.
	if err := r.SetTrustedProxies(opts.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	r.Use(RequestID(), ClientInfo(), Logger(), Recovery(),
		RateLimit(limits, limiterFor),
		BodyLimit(opts.MaxBodyBytes, map[string]int64{
			"/v1/events":                 maxEventsBody,
			"/v1/appstore/notifications": maxAppStoreBody,
		}))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		if err := pool.Ping(c.Request.Context()); err != nil {
			c.JSON(503, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Public share surface (universal links + fallback page), outside /v1.
	if h.Share != nil {
		r.GET("/s/:id", h.Share.Spot)
		r.GET("/.well-known/apple-app-site-association", h.Share.AppleAppSiteAssociation)
	}

	// GET response cache. Versioned entries invalidate when the worker bumps
	// the data version after ingest/weather/crowd/scoring; TTL is the
	// fallback. Nil client = middleware passes through.
	cacheGet := func(ttl time.Duration, versioned bool, normalize ...func(url.Values)) gin.HandlerFunc {
		copts := CacheOptions{TTL: ttl, Versioned: versioned}
		if len(normalize) > 0 {
			copts.Normalize = normalize[0]
		}
		return Cache(opts.Cache, copts)
	}

	v1 := r.Group("/v1")
	{
		if h.Spots != nil {
			spots := v1.Group("/spots")
			spots.GET("", cacheGet(10*time.Minute, true), h.Spots.List)
			spots.GET("/:id", cacheGet(10*time.Minute, true), h.Spots.Get)
			spots.GET("/:id/forecast", cacheGet(10*time.Minute, true), h.Spots.Forecast)
			v1.GET("/packs", cacheGet(30*time.Minute, true), h.Spots.Packs)
		}
		if h.Recommendations != nil {
			v1.GET("/recommendations", cacheGet(10*time.Minute, true, RecNormalize), h.Recommendations.List)
		}
		if h.Devices != nil {
			v1.POST("/devices", h.Devices.Register)
			v1.DELETE("/devices/:id", h.Devices.Delete)
		}
		if h.Reports != nil {
			v1.POST("/spots/:id/reports", h.Reports.SpotReport)
			v1.POST("/spots/:id/crowd-reports", h.Reports.CrowdReport)
			v1.POST("/scores/feedback", h.Reports.ScoreFeedback)
		}
		if h.Alerts != nil {
			v1.GET("/alerts", h.Alerts.List)
			v1.POST("/alerts", h.Alerts.Create)
			v1.PUT("/alerts/:id", h.Alerts.Update)
			v1.DELETE("/alerts/:id", h.Alerts.Delete)
		}
		if h.Digest != nil {
			v1.PUT("/devices/:id/digest", h.Digest.Put)
			v1.GET("/devices/:id/digest", h.Digest.Get)
			v1.DELETE("/devices/:id/digest", h.Digest.Delete)
		}
		if h.Events != nil {
			v1.POST("/events", h.Events.Track)
		}
		if h.Entitlement != nil {
			v1.GET("/devices/:id/entitlement", h.Entitlement.Get)
			v1.POST("/entitlement/link", h.Entitlement.Link)
			v1.POST("/appstore/notifications", h.Entitlement.AppStoreNotification)
		}
		// Other modules register their routes here as they are implemented.
	}

	// Staff surface — disabled unless ADMIN_TOKEN is configured.
	if h.Admin != nil {
		admin := v1.Group("/admin", AdminAudit(), AdminAuth(opts.AdminToken, limiterFor(adminFail, "authfail")))
		{
			admin.GET("/overview", h.Admin.Overview)
			admin.GET("/freshness", h.Admin.Freshness)
			admin.GET("/metrics", h.Admin.Metrics)
			admin.GET("/events", h.Admin.Events)
			admin.GET("/events/breakdown", h.Admin.EventBreakdown)
			admin.GET("/jobs", h.Admin.Jobs)
			admin.GET("/jobs/schedules", h.Admin.JobSchedules)
			admin.PATCH("/jobs/:job/schedule", h.Admin.UpdateJobSchedule)
			admin.GET("/reports", h.Admin.Reports)
			admin.PATCH("/reports/:id", h.Admin.ModerateReport)
			admin.GET("/scoring-config", h.Admin.ScoringConfigs)
			admin.POST("/scoring-config", h.Admin.CreateScoringConfig)
			admin.POST("/scoring-config/:version/activate", h.Admin.ActivateScoringConfig)
			admin.GET("/api-keys", h.Admin.APIKeys)
			admin.POST("/api-keys", h.Admin.CreateAPIKey)
			admin.POST("/api-keys/:id/revoke", h.Admin.RevokeAPIKey)
			admin.POST("/api-keys/:id/grants", h.Admin.AddAPIKeyGrant)
			admin.DELETE("/api-keys/:id/grants", h.Admin.RemoveAPIKeyGrant)
		}
	}

	return r, nil
}
