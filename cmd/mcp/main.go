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

	if err := persistence.RunMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("migrations: %v", err)
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

	if os.Getenv("GIN_MODE") == "" && os.Getenv("APP_ENV") != "production" {
		gin.SetMode(gin.DebugMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	router.Any("/mcp", apiKeyAuth(keysSvc), gin.WrapH(mcpHandler))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
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
