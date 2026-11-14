// Command mcp serves the Pickhour MCP server over Streamable HTTP.
// Agents authenticate with "Authorization: Bearer phk_..." API keys whose
// grants (resource:action pairs, deny-wins) gate each tool.
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
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
	port := os.Getenv("MCP_PORT")
	if port == "" {
		port = "8081"
	}

	ctx := context.Background()

	if cfg.RunMigrations {
		if err := persistence.RunMigrations(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
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

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "pickhour",
		Version: "0.1.0",
	}, nil)
	registerTools(server, spotsSvc, recSvc, keysSvc)

	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)

	// Gin reads its mode when the engine is built: set it first.
	cfg.ConfigureGinMode(gin.SetMode)
	router := gin.New()
	// Trust no proxy by default so X-Forwarded-For cannot spoof the client IP
	// used for the failed-auth throttle.
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
		// No WriteTimeout on purpose: Streamable HTTP answers long-lived
		// responses (SSE streams) and net/http's WriteTimeout is an absolute
		// deadline on the whole response, so any non-zero value would cut
		// streams off mid-flight. Slow-reader abuse is bounded by IdleTimeout
		// and the per-key rate limiter instead.
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
