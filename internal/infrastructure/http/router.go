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

type Options struct {
	TrustedProxies      []string
	MaxBodyBytes        int64
	Limits              *RateLimits
	AdminToken          string
	AdminAllowedOrigins []string
	AdminFailed         *ratelimit.Rule
	LimiterFor          func(ratelimit.Rule, string) ratelimit.Checker
	Cache               *redis.Client
}

const (
	defaultMaxBody = 64 << 10

	maxEventsBody   = 512 << 10
	maxAppStoreBody = 256 << 10
)

type Handlers struct {
	Spots           *SpotsHandler
	Recommendations *RecommendationsHandler
	Devices         *DevicesHandler
	Reports         *ReportsHandler
	Alerts          *AlertsHandler
	Entitlement     *EntitlementHandler
	Admin           *AdminHandler
	AdminAuth       *AdminAuthHandler
	GoAuth          *GoAuthHandler
	Traffic         *TrafficHandler
	Events          *EventsHandler
	Digest          *DigestHandler
	Share           *ShareHandler
}

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

	if err := r.SetTrustedProxies(opts.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	r.Use(RequestID(), ClientInfo(), Logger(), Recovery(), Gzip(),
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

	if h.Share != nil {
		r.GET("/s/:id", h.Share.Spot)
		r.GET("/.well-known/apple-app-site-association", h.Share.AppleAppSiteAssociation)
	}

	cacheGet := func(ttl time.Duration, versioned bool, normalize ...func(url.Values)) gin.HandlerFunc {
		copts := CacheOptions{TTL: ttl, Versioned: versioned}
		if len(normalize) > 0 {
			copts.Normalize = normalize[0]
		}
		return Cache(opts.Cache, copts)
	}

	cacheGetWeather := func(ttl time.Duration, normalize func(url.Values)) gin.HandlerFunc {
		return Cache(opts.Cache, CacheOptions{TTL: ttl, Versioned: true, WeatherVersioned: true, Normalize: normalize})
	}

	v1 := r.Group("/v1")
	{
		if h.Spots != nil {
			spots := v1.Group("/spots")
			spots.GET("", cacheGet(10*time.Minute, true, SpotsNormalize), h.Spots.List)
			spots.GET("/:id", cacheGetWeather(10*time.Minute, DropQueryParams), h.Spots.Get)
			spots.GET("/:id/forecast", cacheGetWeather(10*time.Minute, DropQueryParams), h.Spots.Forecast)
			v1.GET("/packs", cacheGet(30*time.Minute, true, DropQueryParams), h.Spots.Packs)
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

	}

	if h.Admin != nil || h.AdminAuth != nil {
		var auditRec AuditRecorder
		if h.Admin != nil {
			auditRec = h.Admin
		}
		admin := v1.Group("/admin", AdminAudit(auditRec), AdminCSRF(opts.AdminAllowedOrigins))
		var sessions SessionAuthenticator
		if h.AdminAuth != nil {
			sessions = h.AdminAuth.Service()
		}
		authed := admin.Group("", AdminAuth(opts.AdminToken, sessions, limiterFor(adminFail, "authfail")))
		if h.AdminAuth != nil {

			admin.POST("/auth/login", h.AdminAuth.Login)
			admin.POST("/auth/totp", h.AdminAuth.TOTP)
			admin.POST("/auth/enroll", h.AdminAuth.Enroll)

			authed.GET("/auth/me", h.AdminAuth.Me)
			authed.POST("/auth/logout", h.AdminAuth.Logout)
			authed.POST("/auth/password", h.AdminAuth.Password)
			authed.POST("/auth/recovery-codes", h.AdminAuth.RecoveryCodes)
			authed.GET("/auth/sessions", h.AdminAuth.Sessions)
			authed.POST("/auth/sessions/revoke-others", h.AdminAuth.RevokeOtherSessions)
		}
		if h.GoAuth != nil {
			authed.GET("/integrations/google/connect", h.GoAuth.Connect)
			authed.GET("/integrations/google/callback", h.GoAuth.Callback)
			authed.GET("/integrations/google", h.GoAuth.Status)
			authed.PUT("/integrations/google/config", h.GoAuth.Config)
			authed.GET("/integrations/google/resources", h.GoAuth.Resources)
			authed.DELETE("/integrations/google", h.GoAuth.Disconnect)
		}
		if h.Traffic != nil {
			authed.GET("/traffic/gsc", h.Traffic.Gsc)
			authed.GET("/traffic/ga4", h.Traffic.Ga4)
		}
		if h.Admin != nil {
			authed.GET("/overview", h.Admin.Overview)
			authed.GET("/freshness", h.Admin.Freshness)
			authed.GET("/metrics", h.Admin.Metrics)
			authed.GET("/events", h.Admin.Events)
			authed.GET("/events/breakdown", h.Admin.EventBreakdown)
			authed.GET("/cache", h.Admin.CacheStats)
			authed.GET("/audit", h.Admin.AuditEvents)
			authed.GET("/jobs", h.Admin.Jobs)
			authed.GET("/jobs/runs/:id", h.Admin.JobRun)
			authed.GET("/jobs/schedules", h.Admin.JobSchedules)
			authed.PATCH("/jobs/:job/schedule", h.Admin.UpdateJobSchedule)
			authed.POST("/jobs/:job/run", h.Admin.RunJobNow)
			authed.GET("/plan-limits", h.Admin.PlanLimits)
			authed.PUT("/plan-limits/:plan/:feature", h.Admin.UpdatePlanLimit)
			authed.GET("/city-packs", h.Admin.CityPacks)
			authed.GET("/city-packs/:id", h.Admin.CityPackDetail)
			authed.PATCH("/city-packs/:id", h.Admin.UpdateCityPack)
			authed.GET("/spots", h.Admin.Spots)
			authed.PATCH("/spots/:id", h.Admin.UpdateSpot)
			authed.GET("/reports", h.Admin.Reports)
			authed.PATCH("/reports/:id", h.Admin.ModerateReport)
			authed.GET("/scoring-config", h.Admin.ScoringConfigs)
			authed.POST("/scoring-config", h.Admin.CreateScoringConfig)
			authed.POST("/scoring-config/:version/activate", h.Admin.ActivateScoringConfig)
			authed.GET("/api-keys", h.Admin.APIKeys)
			authed.POST("/api-keys", h.Admin.CreateAPIKey)
			authed.POST("/api-keys/:id/revoke", h.Admin.RevokeAPIKey)
			authed.POST("/api-keys/:id/grants", h.Admin.AddAPIKeyGrant)
			authed.DELETE("/api-keys/:id/grants", h.Admin.RemoveAPIKeyGrant)
		}
	}

	return r, nil
}
