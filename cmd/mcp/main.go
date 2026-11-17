package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	port := cfg.MCPPort

	ctx := context.Background()

	if cfg.RunMigrations {
		if err := persistence.RunMigrations(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
	}

	// Ops tools run admin aggregates (e.g. count(DISTINCT spot_id) over
	// t_spot_scores) that exceed the API's 5s statement budget — give the
	// MCP pool its own, larger timeout.
	mcpParams := cfg.DBRuntimeParams()
	mcpParams["statement_timeout"] = cfg.MCPStatementTimeout
	pool, err := persistence.NewPool(ctx, cfg.DatabaseURL, mcpParams, cfg.DBMaxConns)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	queries := persistence.New(pool)
	spotsSvc := spots.NewService(persistence.NewSpotsRepository(queries))
	recSvc := recommendations.NewService(persistence.NewRecommendationsRepo(queries))
	keysSvc := apikeys.NewService(persistence.NewAPIKeysRepo(queries, pool))
	adminSvc := admin.NewService(persistence.NewAdminRepo(queries, pool))

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "pickhour",
		Version: "0.1.0",
	}, nil)
	registerTools(server, spotsSvc, recSvc, keysSvc)
	registerOpsTools(server, adminSvc, keysSvc)

	if cfg.MCPRODatabaseURL != "" {
		roPool, err := persistence.NewPool(ctx, cfg.MCPRODatabaseURL, map[string]string{
			"statement_timeout": "30s",
			"jit":               "off",
		}, 4)
		if err != nil {
			log.Printf("mcp db_query disabled: ro pool: %v", err)
		} else if err := roPool.Ping(ctx); err != nil {
			log.Printf("mcp db_query disabled: ro pool ping: %v", err)
			roPool.Close()
		} else {
			defer roPool.Close()
			registerDBQueryTool(server, keysSvc, roPool)
		}
	} else {
		log.Print("mcp db_query disabled: MCP_RO_DATABASE_URL not set")
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)

	cfg.ConfigureGinMode(gin.SetMode)
	router := gin.New()

	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	router.Use(gin.Recovery())
	authFail := ratelimit.New(ratelimit.PerMinute(float64(cfg.RateAuthFailPerMin), cfg.RateAuthFailPerMin))
	router.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	router.Any("/mcp", apiKeyAuth(keysSvc, newKeyLimiter(), authFail), gin.WrapH(mcpHandler))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,

		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	go func() {
		log.Printf("pickhour mcp listening on :%s/mcp", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Println("shutdown:", err)
	}
	log.Println("server stopped")
}
