package http

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
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
	Getaways        *GetawaysHandler
	Devices         *DevicesHandler
	DeviceSvc       *devices.Service
	Reports         *ReportsHandler
	Alerts          *AlertsHandler
	Entitlement     *EntitlementHandler
	Admin           *AdminHandler
	AdminAuth       *AdminAuthHandler
	GoAuth          *GoAuthHandler
	Traffic         *TrafficHandler
	Events          *EventsHandler
	Digest          *DigestHandler
	Nudge           *NudgeHandler
	Share           *ShareHandler
	Weather         *WeatherHandler
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

	// Admin aggregate reads are global (not per-user), so they can share a
	// cached response. Versioned by AdminAggVersionKey: POST /cache/refresh
	// bumps it without touching the public caches. Bearer-token requests
	// (scripts/CI) carry Authorization and bypass the cache entirely.
	adminCacheGet := func(ttl time.Duration) gin.HandlerFunc {
		return Cache(opts.Cache, CacheOptions{
			TTL: ttl, Versioned: true, VersionKey: cache.AdminAggVersionKey,
			BypassOnAuth: true,
		})
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
		if h.Getaways != nil {
			// Tier runs before the cache so Free and Plus never share an entry.
			v1.GET("/getaways", h.Getaways.Tier, cacheGet(30*time.Minute, true, GetawaysNormalize), h.Getaways.List)
		}
		if h.Weather != nil {
			v1.GET("/weather/nearby", cacheGet(15*time.Minute, false, NearbyNormalize), h.Weather.Nearby)
		}
		if h.Devices != nil {
			v1.POST("/devices", h.Devices.Register)
			if h.DeviceSvc != nil {
				v1.DELETE("/devices/:id", deviceAuthParam(h.DeviceSvc), h.Devices.Delete)
			} else {
				v1.DELETE("/devices/:id", h.Devices.Delete)
			}
		}
		if h.Reports != nil {
			devAuth := deviceAuthHeader
			if h.DeviceSvc != nil {
				v1.POST("/spots/:id/reports", devAuth(h.DeviceSvc), h.Reports.SpotReport)
				v1.POST("/spots/:id/crowd-reports", devAuth(h.DeviceSvc), h.Reports.CrowdReport)
				v1.POST("/scores/feedback", devAuth(h.DeviceSvc), h.Reports.ScoreFeedback)
			} else {
				v1.POST("/spots/:id/reports", h.Reports.SpotReport)
				v1.POST("/spots/:id/crowd-reports", h.Reports.CrowdReport)
				v1.POST("/scores/feedback", h.Reports.ScoreFeedback)
			}
		}
		if h.Alerts != nil {
			if h.DeviceSvc != nil {
				auth := deviceAuthHeader(h.DeviceSvc)
				v1.GET("/alerts", auth, h.Alerts.List)
				v1.POST("/alerts", auth, h.Alerts.Create)
				v1.PUT("/alerts/:id", auth, h.Alerts.Update)
				v1.DELETE("/alerts/:id", auth, h.Alerts.Delete)
			} else {
				v1.GET("/alerts", h.Alerts.List)
				v1.POST("/alerts", h.Alerts.Create)
				v1.PUT("/alerts/:id", h.Alerts.Update)
				v1.DELETE("/alerts/:id", h.Alerts.Delete)
			}
		}
		if h.Digest != nil {
			if h.DeviceSvc != nil {
				auth := deviceAuthParam(h.DeviceSvc)
				v1.PUT("/devices/:id/digest", auth, h.Digest.Put)
				v1.GET("/devices/:id/digest", auth, h.Digest.Get)
				v1.DELETE("/devices/:id/digest", auth, h.Digest.Delete)
			} else {
				v1.PUT("/devices/:id/digest", h.Digest.Put)
				v1.GET("/devices/:id/digest", h.Digest.Get)
				v1.DELETE("/devices/:id/digest", h.Digest.Delete)
			}
		}
		if h.Nudge != nil {
			if h.DeviceSvc != nil {
				auth := deviceAuthParam(h.DeviceSvc)
				v1.PUT("/devices/:id/nudge", auth, h.Nudge.Put)
				v1.GET("/devices/:id/nudge", auth, h.Nudge.Get)
				v1.DELETE("/devices/:id/nudge", auth, h.Nudge.Delete)
			} else {
				v1.PUT("/devices/:id/nudge", h.Nudge.Put)
				v1.GET("/devices/:id/nudge", h.Nudge.Get)
				v1.DELETE("/devices/:id/nudge", h.Nudge.Delete)
			}
		}
		if h.Events != nil {
			if h.DeviceSvc != nil {
				v1.POST("/events", deviceAuthHeader(h.DeviceSvc), h.Events.Track)
			} else {
				v1.POST("/events", h.Events.Track)
			}
		}
		if h.Entitlement != nil {
			if h.DeviceSvc != nil {
				v1.GET("/devices/:id/entitlement", deviceAuthParam(h.DeviceSvc), h.Entitlement.Get)
				v1.POST("/entitlement/link", deviceAuthHeader(h.DeviceSvc), h.Entitlement.Link)
			} else {
				v1.GET("/devices/:id/entitlement", h.Entitlement.Get)
				v1.POST("/entitlement/link", h.Entitlement.Link)
			}
			v1.POST("/appstore/notifications", h.Entitlement.AppStoreNotification)
		}

	}

	if h.Admin != nil || h.AdminAuth != nil {
		var auditRec AuditRecorder
		if h.Admin != nil {
			auditRec = h.Admin
		}
		admin := v1.Group("/admin", AdminAudit(auditRec), AdminCSRF(opts.AdminAllowedOrigins, opts.TrustedProxies))
		var sessions SessionAuthenticator
		if h.AdminAuth != nil {
			sessions = h.AdminAuth.Service()
		}
		// Any successful admin mutation invalidates the aggregate cache so
		// overview/metrics/events don't serve stale numbers until TTL.
		adminMutate := func(c *gin.Context) {
			c.Next()
			if opts.Cache == nil || c.Request.Method == http.MethodGet || c.Writer.Status() >= 400 {
				return
			}
			_ = cache.BumpAdminAgg(c.Request.Context(), opts.Cache)
		}
		authed := admin.Group("", AdminAuth(opts.AdminToken, sessions, limiterFor(adminFail, "authfail")), adminMutate)
		if h.AdminAuth != nil {

			admin.POST("/auth/login", h.AdminAuth.Login)
			admin.POST("/auth/totp", h.AdminAuth.TOTP)
			admin.POST("/auth/enroll", h.AdminAuth.Enroll)
			admin.POST("/auth/refresh", h.AdminAuth.Refresh)
			admin.POST("/auth/logout", h.AdminAuth.Logout)

			authed.GET("/auth/me", h.AdminAuth.Me)
			authed.POST("/auth/password", h.AdminAuth.Password)
			authed.POST("/auth/recovery-codes", h.AdminAuth.RecoveryCodes)
			authed.GET("/auth/sessions", h.AdminAuth.Sessions)
			authed.POST("/auth/sessions/revoke-others", h.AdminAuth.RevokeOtherSessions)

			authed.GET("/users", h.AdminAuth.ListUsers)
			authed.POST("/users", h.AdminAuth.CreateUser)
			authed.PATCH("/users/:email", h.AdminAuth.UpdateUser)
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
			authed.GET("/overview", adminCacheGet(5*time.Minute), h.Admin.Overview)
			authed.GET("/freshness", h.Admin.Freshness)
			authed.GET("/metrics", adminCacheGet(5*time.Minute), h.Admin.Metrics)
			authed.GET("/events", adminCacheGet(5*time.Minute), h.Admin.Events)
			authed.GET("/events/breakdown", adminCacheGet(5*time.Minute), h.Admin.EventBreakdown)
			authed.GET("/cache", h.Admin.CacheStats)
			authed.POST("/cache/refresh", h.Admin.RefreshCache)
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
			authed.GET("/spots/:id", h.Admin.SpotDetail)
			authed.PATCH("/spots/:id", h.Admin.UpdateSpot)
			authed.GET("/reports", h.Admin.Reports)
			authed.GET("/reports/history", h.Admin.SpotReportsHistory)
			authed.GET("/reports/:id", h.Admin.SpotReportDetail)
			authed.PATCH("/reports/:id", h.Admin.ModerateReport)
			authed.GET("/crowd-reports", h.Admin.CrowdReports)
			authed.GET("/score-feedbacks", h.Admin.ScoreFeedbacks)
			authed.GET("/devices", h.Admin.Devices)
			authed.GET("/devices/:id", h.Admin.DeviceDetail)
			authed.GET("/alert-deliveries", h.Admin.AlertDeliveries)
			authed.GET("/apple-notifications", h.Admin.AppleNotifications)
			authed.GET("/providers/health", h.Admin.ProviderHealth)
			authed.GET("/ingest/features", h.Admin.IngestFeatures)
			authed.POST("/ingest/packs/:slug/run", h.Admin.RunPackIngest)
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
