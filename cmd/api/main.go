package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	infrahttp "github.com/thanhhoa35142/be-example/internal/infrastructure/http"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/modules/devices"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/events"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/reports"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
	platformlog "github.com/thanhhoa35142/be-example/internal/platform/log"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
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
	spotsRepo := persistence.NewSpotsRepository(queries)
	spotsSvc := spots.NewService(spotsRepo)
	recSvc := recommendations.NewService(persistence.NewRecommendationsRepo(queries))
	devicesSvc := devices.NewService(persistence.NewDevicesRepo(queries))
	reportsSvc := reports.NewService(persistence.NewReportsRepo(queries))
	entitlementSvc := entitlement.NewService(persistence.NewEntitlementRepo(queries))
	logger := platformlog.New("api")
	alertsRepo := persistence.NewAlertsRepo(queries)
	apnsClient, err := notifier.ClientFromEnv()
	if err != nil {
		logger.Warn("APNs disabled", "error", err)
	}
	alertsSvc := alerts.NewService(
		alertsRepo,
		entitlementSvc,
		notifier.NewSender(logger, alertsRepo, apnsClient),
	)

	adminSvc := admin.NewService(persistence.NewAdminRepo(queries))
	keysSvc := apikeys.NewService(persistence.NewAPIKeysRepo(queries, pool))
	eventsSvc := events.NewService(persistence.NewEventsRepo(queries))

	router := infrahttp.NewRouter(pool, infrahttp.Handlers{
		Spots:           infrahttp.NewSpotsHandler(spotsSvc),
		Recommendations: infrahttp.NewRecommendationsHandler(recSvc),
		Devices:         infrahttp.NewDevicesHandler(devicesSvc),
		Reports:         infrahttp.NewReportsHandler(reportsSvc),
		Alerts:          infrahttp.NewAlertsHandler(alertsSvc),
		Entitlement:     infrahttp.NewEntitlementHandler(entitlementSvc),
		Admin:           infrahttp.NewAdminHandler(adminSvc, keysSvc),
		Events:          infrahttp.NewEventsHandler(eventsSvc),
	})

	if os.Getenv("GIN_MODE") == "" && os.Getenv("APP_ENV") != "production" {
		gin.SetMode(gin.DebugMode)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("server listening on :%s", cfg.Port)
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
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
	log.Println("server stopped")
}
