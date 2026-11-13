package http

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
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
}

func NewRouter(pool *pgxpool.Pool, h Handlers) *gin.Engine {
	r := gin.New()
	r.Use(RequestID(), ClientInfo(), Logger(), Recovery())

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

	v1 := r.Group("/v1")
	{
		if h.Spots != nil {
			spots := v1.Group("/spots")
			spots.GET("", h.Spots.List)
			spots.GET("/:id", h.Spots.Get)
			spots.GET("/:id/forecast", h.Spots.Forecast)
			v1.GET("/packs", h.Spots.Packs)
		}
		if h.Recommendations != nil {
			v1.GET("/recommendations", h.Recommendations.List)
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
		admin := v1.Group("/admin", AdminAuth())
		{
			admin.GET("/overview", h.Admin.Overview)
			admin.GET("/freshness", h.Admin.Freshness)
			admin.GET("/metrics", h.Admin.Metrics)
			admin.GET("/events", h.Admin.Events)
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

	return r
}
